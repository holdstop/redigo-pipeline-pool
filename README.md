Redigo-Pipline-Pool
======

Simple Go Redis pipline pool client base on [Redigo](https://github.com/gomodule/redigo).

This is a connection free Redis pool. It handles all connection operations by itself. You don't need to deal with Redis connection.

This pool use pipline to execute Redis commands. Multiple commands can execute on one connection at the same time. So you don't need too many connections.

You can use `delay` and `maxPendingSize` to compress multiple command into fewer TCP packages. This can reduce network traffic.

Why pool?
------------

You can use one connection with pipline to do the same thing. But multiple connections can help you use multiple cores of CPU and multi-queue of network interface controller to improve performance.

Installation
------------

Install Redigo-Pipline-Pool using the "go get" command:

```
go get github.com/holdstop/redigo-pipline-pool/pool
```

Usage
------------

Import Redigo-Pipline-Pool and Redigo.

```go
import (
	"github.com/holdstop/redigo-pipline-pool/pool"
	"github.com/gomodule/redigo/redis"
)
```

Use the pool.NewPool to create a new pool.

```go
func NewPool(dial func() (redis.Conn, error), connSize int, reconnectInterval time.Duration, delay time.Duration, maxPendingSize int, maxWaitingSize int) (Pool, error)
```

```go
p, err := pool.NewPool(
	func() (redis.Conn, error) {
		return redis.Dial("tcp", "127.0.0.1:6379")
	}, // Function return a Redis connection.
	5, // Number of Redis connections in the pool.
	1 * time.Second, // Reconnect interval when connection encountered non-recoverable error.
	1 * time.Millisecond, // The longest delay time waiting for the number of buffered requests to be maxPendingSize before flush to the Redis server.
	100, // Maximum number of requests buffered before flush to the Redis server. (per connection)
	500, //Maximum number of unreturned requests waited before send new requests. (per connection)
)
```

Use Do and DoScript to execute Redis commands.

```go
Do(cmd string, args ...interface{}) (interface{}, error)
DoScript(script *redis.Script, keysAndArgs ...interface{}) (interface{}, error)
```

```go
reply, err := redis.String(p.Do("SET", "foo", "bar"))
fmt.Println(reply, err)
reply, err = redis.String(p.Do("GET", "foo"))
fmt.Println(reply, err)

script := redis.NewScript(1, "return redis.call(\"GET\", KEYS[1])")
reply, err = redis.String(p.DoScript(script, "foo"))
fmt.Println(reply, err)
```

Use Multi and DoMulti to execute Redis transactions.

```go
type Multi struct {
}
func (multi *Multi) Do(cmd string, args ...interface{})
DoScript(script *redis.Script, keysAndArgs ...interface{}) (interface{}, error)
```

```go
multi := pool.Multi{}
multi.Do("SET", "foo", "bar")
multi.Do("GET", "foo")
fmt.Println(redis.Strings(p.DoMulti(multi)))
```

Use Close to close the pool

```go
p.Close()
```

Supported commands
------------

Supports commands group `Geo` `Hashes` `Keys` `HyperLogLog` `Lists` `Sets` `Sorted Sets` `Strings` on [https://redis.io/commands](https://redis.io/commands), without command `WAIT` `MIGRATE` in group `Keys`.
