package pool

import (
	"fmt"
	"time"
	"sync"
	"errors"
	"strings"
	"math/rand"
	"github.com/gomodule/redigo/redis"
)

type Pool interface {
	Close()
	Do(cmd string, args ...interface{}) (interface{}, error)
	DoMulti(multi Multi) (interface{}, error)
	DoScript(script *redis.Script, keysAndArgs ...interface{}) (interface{}, error)
}

type pool struct {
	dial func() (redis.Conn, error)
	connSize int
	reconnectInterval time.Duration
	delay time.Duration
	maxPendingSize int
	maxWaitingSize int

	rand *rand.Rand
	lastReconnectFailedTime time.Time
	randMutex sync.Mutex
	reconnectMutex sync.Mutex
	closeRWMutex sync.RWMutex

	conns []redis.Conn
	doChs []chan cmdAndChObj
	replyChs []chan chan replyObj
	mutexes []sync.Mutex
}

func NewPool(dial func() (redis.Conn, error), connSize int, reconnectInterval time.Duration, delay time.Duration, maxPendingSize int, maxWaitingSize int) (Pool, error) {
	if (connSize <= 0) {
		return nil, errors.New("connSize must greater than 0")
	}

	p := &pool{
		dial: dial,
		connSize: connSize,
		reconnectInterval: reconnectInterval,
		delay: delay,
		maxPendingSize: maxPendingSize,
		maxWaitingSize: maxWaitingSize,
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
		conns: make([]redis.Conn, connSize),
		doChs: make([]chan cmdAndChObj, connSize),
		replyChs: make([]chan chan replyObj, connSize),
		mutexes: make([]sync.Mutex, connSize),
	}

	for i := 0; i < p.connSize; i++ {
		err := p.connect(i)
		if nil != err {
			p.Close()
			return nil, err
		}
	}

	return p, nil
}

func (p *pool) Close() {
	p.closeRWMutex.Lock()
	defer p.closeRWMutex.Unlock()

	for i := 0; i < p.connSize; i++ {
		p.close(i)
	}

	p.conns = nil
}

func (p *pool) Do(cmd string, args ...interface{}) (interface{}, error) {
	p.closeRWMutex.RLock()
	defer p.closeRWMutex.RUnlock()

	if nil == p.conns {
		return nil, errors.New("Pool has already been closed")
	}

	ch := make(chan replyObj)
	defer close(ch)

	p.randMutex.Lock()
	i := p.rand.Intn(p.connSize)
	p.randMutex.Unlock()

	p.mutexes[i].Lock()

	err := p.check(i)
	if nil != err {
		p.mutexes[i].Unlock()
		return nil, err
	}

	doCh := p.doChs[i]
	doCh <- cmdAndChObj{class: "CMD", cmd: cmdObj{cmd, args}, ch: ch}

	p.mutexes[i].Unlock()

	reply := <-ch
	return reply.reply, reply.err
}

func (p *pool) DoMulti(multi Multi) (interface{}, error) {
	if 0 == len(multi.cmds) {
		return nil, errors.New("Empty multi")
	}

	p.closeRWMutex.RLock()
	defer p.closeRWMutex.RUnlock()

	if nil == p.conns {
		return nil, errors.New("Pool has already been closed")
	}

	ch := make(chan replyObj)
	defer close(ch)

	p.randMutex.Lock()
	i := p.rand.Intn(p.connSize)
	p.randMutex.Unlock()

	p.mutexes[i].Lock()

	err := p.check(i)
	if nil != err {
		p.mutexes[i].Unlock()
		return nil, err
	}

	doCh := p.doChs[i]
	doCh <- cmdAndChObj{class: "CMD", cmd: cmdObj{cmd: "MULTI"}}
	for _, cmd := range multi.cmds {
		doCh <- cmdAndChObj{class: "CMD", cmd: cmd}
	}
	doCh <- cmdAndChObj{class: "CMD", cmd: cmdObj{cmd: "EXEC"}, ch: ch}

	p.mutexes[i].Unlock()

	reply := <-ch
	return reply.reply, reply.err
}

func (p *pool) DoScript(script *redis.Script, keysAndArgs ...interface{}) (interface{}, error) {
	if nil == script {
		return nil, errors.New("Nil script")
	}

	p.closeRWMutex.RLock()
	defer p.closeRWMutex.RUnlock()

	if nil == p.conns {
		return nil, errors.New("Pool has already been closed")
	}

	ch := make(chan replyObj)
	defer close(ch)

	p.randMutex.Lock()
	i := p.rand.Intn(p.connSize)
	p.randMutex.Unlock()

	p.mutexes[i].Lock()

	err := p.check(i)
	if nil != err {
		p.mutexes[i].Unlock()
		return nil, err
	}

	doCh := p.doChs[i]
	doCh <- cmdAndChObj{class: "EVALSHA", script: scriptObj{script, keysAndArgs}, ch: ch}

	p.mutexes[i].Unlock()

	reply := <-ch

	if nil != reply.err && strings.HasPrefix(reply.err.Error(), "NOSCRIPT ") {
		p.randMutex.Lock()
		i := p.rand.Intn(p.connSize)
		p.randMutex.Unlock()

		p.mutexes[i].Lock()

		err := p.check(i)
		if nil != err {
			p.mutexes[i].Unlock()
			return nil, err
		}

		doCh := p.doChs[i]
		doCh <- cmdAndChObj{class: "EVAL", script: scriptObj{script, keysAndArgs}, ch: ch}

		p.mutexes[i].Unlock()

		reply = <-ch
	}

	return reply.reply, reply.err
}

func (p *pool) check(i int) error {
	if nil == p.conns[i] || nil != p.conns[i].Err() {
		err := p.reconnect(i)
		if nil != err {
			return err
		}
	}

	return nil
}

func (p *pool) reconnect(i int) error {
	if nil != p.conns[i] {
		p.close(i)
	}

	p.reconnectMutex.Lock()
	defer p.reconnectMutex.Unlock()

	if p.lastReconnectFailedTime.Add(p.reconnectInterval).After(time.Now()) {
		return fmt.Errorf("reconnect failed %s ago, skipped current reconnection", p.reconnectInterval.String())
	}

	err := p.connect(i)
	if nil != err {
		p.lastReconnectFailedTime = time.Now()
		return err
	} else {
		p.lastReconnectFailedTime = time.Time{}
		return nil
	}
}

func (p *pool) connect(i int) error {
	conn, err := p.dial()
	if nil != err {
		return err
	}

	p.conns[i] = conn
	p.doChs[i] = make(chan cmdAndChObj, p.maxPendingSize)
	p.replyChs[i] = make(chan chan replyObj, p.maxWaitingSize)
	go loopReceive(p.conns[i], p.replyChs[i])
	go loopSend(p.conns[i], p.doChs[i], p.replyChs[i], p.delay, p.maxPendingSize)

	return nil
}

func (p *pool) close(i int) {
	if (nil != p.conns[i]) {
		p.conns[i].Close()
		p.conns[i] = nil
	}
	if (nil != p.doChs[i]) {
		close(p.doChs[i])
		p.doChs[i] = nil
	}
	if (nil != p.replyChs[i]) {
		close(p.replyChs[i])
		p.replyChs[i] = nil
	}
}