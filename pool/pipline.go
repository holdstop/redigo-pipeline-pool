package pool

import (
	"time"
	"errors"
	"github.com/gomodule/redigo/redis"
)

type cmdObj struct {
	cmd string
	args []interface{}
}

type scriptObj struct {
	script *redis.Script
	keysAndArgs []interface{}
}

type replyObj struct {
	reply interface{}
	err error
}

type cmdAndChObj struct {
	class string // CMD, EVAL, EVALSHA
	cmd cmdObj
	script scriptObj
	ch chan replyObj
}

func loopReceive(conn redis.Conn, replyCh chan chan replyObj) {
	for {
		ch, ok := <-replyCh
		if !ok {
			return
		}
		reply, err := conn.Receive()
		if nil != ch {
			ch <- replyObj{reply, err}
		}
	}
}

func loopSend(conn redis.Conn, doCh chan cmdAndChObj, replyCh chan chan replyObj, delay time.Duration, maxPendingSize int) {
	OuterFor:
	for {
		obj, ok := <-doCh
		if !ok {
			return
		}

		if !send(conn, obj) {
			continue OuterFor
		}
		chs := []chan replyObj{obj.ch}

		if (maxPendingSize <= 1) {
			flush(conn, chs, replyCh)
			continue OuterFor
		}

		delayFlag := true

		InnerFor:
		for {
			select {
			case obj, ok = <-doCh:
				if !ok {
					flush(conn, chs, replyCh)
					return
				}

				if !send(conn, obj) {
					continue InnerFor
				}
				chs = append(chs, obj.ch)

				if len(chs) >= maxPendingSize {
					flush(conn, chs, replyCh)
					break InnerFor
				}
			default:
				if delayFlag && delay > 0 {
					time.Sleep(delay)
					delayFlag = false
				} else {
					flush(conn, chs, replyCh)
					break InnerFor
				}
			}
		}
	}
}

func send(conn redis.Conn, obj cmdAndChObj) bool {
	var err error

	switch obj.class {
	case "CMD":
		err = conn.Send(obj.cmd.cmd, obj.cmd.args...)
	case "EVAL":
		err = obj.script.script.Send(conn, obj.script.keysAndArgs...)
	case "EVALSHA":
		err = obj.script.script.SendHash(conn, obj.script.keysAndArgs...)
	default:
		err = errors.New("Unsupported class")
	}

	if nil != err {
		obj.ch <- replyObj{nil, err}
		return false
	} else {
		return true
	}
}

func flush(conn redis.Conn, chs []chan replyObj, replyCh chan chan replyObj) {
	err := conn.Flush()
	if nil != err {
		for _, ch := range chs {
			ch <- replyObj{nil, err}
		}
		return
	}

	for _, ch := range chs {
		replyCh <- ch
	}
}