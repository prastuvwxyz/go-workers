package workers

import (
	"fmt"
	"sync"
	"time"

	"github.com/customerio/gospec"
	. "github.com/customerio/gospec"
)

type customMid struct {
	trace []string
	Base  string
	mutex sync.Mutex
}

func (m *customMid) Call(queue string, message *Msg, next func() bool) (result bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.trace = append(m.trace, m.Base+"1")
	result = next()
	m.trace = append(m.trace, m.Base+"2")
	return
}

func (m *customMid) Trace() []string {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	t := make([]string, len(m.trace))
	copy(t, m.trace)

	return t
}

func ManagerSpec(c gospec.Context) {
	processed := make(chan *Args)

	testJob := (func(message *Msg) {
		processed <- message.Args()
	})

	was := Config.Namespace
	Config.Namespace = "{worker}:"

	c.Specify("newManager", func() {
		c.Specify("sets queue with namespace", func() {
			manager := newManager("myqueue", testJob, 10)
			c.Expect(manager.queue, Equals, "{worker}:queue:myqueue")
		})

		c.Specify("sets job function", func() {
			manager := newManager("myqueue", testJob, 10)
			c.Expect(fmt.Sprint(manager.job), Equals, fmt.Sprint(testJob))
		})

		c.Specify("sets worker concurrency", func() {
			manager := newManager("myqueue", testJob, 10)
			c.Expect(manager.concurrency, Equals, 10)
		})

		c.Specify("no per-manager middleware means 'use global Middleware object'", func() {
			manager := newManager("myqueue", testJob, 10)
			c.Expect(manager.mids, Equals, Middleware)
		})

		c.Specify("per-manager middlewares create separate middleware chains", func() {
			mid1 := customMid{Base: "0"}
			manager := newManager("myqueue", testJob, 10, &mid1)
			c.Expect(manager.mids, Not(Equals), Middleware)
			c.Expect(len(manager.mids.actions), Equals, len(Middleware.actions)+1)
		})

	})

	c.Specify("manage", func() {
		message, _ := NewMsg("{\"foo\":\"bar\",\"args\":[\"foo\",\"bar\"]}")
		message2, _ := NewMsg("{\"foo\":\"bar2\",\"args\":[\"foo\",\"bar2\"]}")

		c.Specify("coordinates processing of queue messages", func() {
			manager := newManager("manager1", testJob, 10)

			Config.Redis.LPush("{worker}:queue:manager1", message.ToJson())
			Config.Redis.LPush("{worker}:queue:manager1", message2.ToJson())

			manager.start()

			c.Expect(<-processed, Equals, message.Args())
			c.Expect(<-processed, Equals, message2.Args())

			manager.quit()

			len, _ := Config.Redis.LLen("{worker}:queue:manager1").Result()
			c.Expect(len, Equals, int64(0))
		})

		c.Specify("drain queue completely on exit", func() {
			sentinel, _ := NewMsg("{\"foo\":\"bar2\",\"args\":\"sentinel\"}")

			drained := false

			slowJob := (func(message *Msg) {
				if message.ToJson() == sentinel.ToJson() {
					drained = true
				} else {
					processed <- message.Args()
				}

				time.Sleep(1 * time.Second)
			})
			manager := newManager("manager1", slowJob, 10)

			for i := 0; i < 9; i++ {
				Config.Redis.LPush("{worker}:queue:manager1", message.ToJson())
			}
			Config.Redis.LPush("{worker}:queue:manager1", sentinel.ToJson())

			manager.start()
			for i := 0; i < 9; i++ {
				<-processed
			}
			manager.quit()

			len, _ := Config.Redis.LLen("{worker}:queue:manager1").Result()
			c.Expect(len, Equals, int64(0))
			c.Expect(drained, Equals, true)
		})

		c.Specify("per-manager middlwares are called separately, global middleware is called in each manager", func() {
			mid1 := customMid{Base: "1"}
			mid2 := customMid{Base: "2"}
			mid3 := customMid{Base: "3"}

			oldMiddleware := Middleware
			Middleware = NewMiddleware()
			Middleware.Append(&mid1)

			manager1 := newManager("manager1", testJob, 10)
			manager2 := newManager("manager2", testJob, 10, &mid2)
			manager3 := newManager("manager3", testJob, 10, &mid3)

			Config.Redis.LPush("{worker}:queue:manager1", message.ToJson())
			Config.Redis.LPush("{worker}:queue:manager2", message.ToJson())
			Config.Redis.LPush("{worker}:queue:manager3", message.ToJson())

			manager1.start()
			manager2.start()
			manager3.start()

			<-processed
			<-processed
			<-processed

			Middleware = oldMiddleware

			c.Expect(
				arrayCompare(mid1.Trace(), []string{"11", "12", "11", "12", "11", "12"}),
				IsTrue,
			)
			c.Expect(
				arrayCompare(mid2.Trace(), []string{"21", "22"}),
				IsTrue,
			)
			c.Expect(
				arrayCompare(mid3.Trace(), []string{"31", "32"}),
				IsTrue,
			)

			manager1.quit()
			manager2.quit()
			manager3.quit()
		})

		c.Specify("prepare stops fetching new messages from queue", func() {
			manager := newManager("manager2", testJob, 10)
			manager.start()

			manager.prepare()

			Config.Redis.LPush("{worker}:queue:manager2", message.ToJson())
			Config.Redis.LPush("{worker}:queue:manager2", message2.ToJson())

			manager.quit()

			len, _ := Config.Redis.LLen("{worker}:queue:manager2").Result()
			c.Expect(len, Equals, int64(2))
		})
	})

	Config.Namespace = was
}
