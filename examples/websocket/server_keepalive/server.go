package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

var (
	KeepaliveTime    = time.Second * 5
	KeepaliveTimeout = KeepaliveTime + time.Second*3
)

var clientMgr *ClientMgr

type ClientMgr struct {
	clients sync.Map
	mux     sync.Mutex
	chStop  chan struct{}
	// clients       map[*websocket.Conn]struct{}
	keepaliveTime time.Duration
}

func NewClientMgr(keepaliveTime time.Duration) *ClientMgr {
	return &ClientMgr{
		chStop: make(chan struct{}),
		// clients:       map[*websocket.Conn]struct{}{},
		keepaliveTime: keepaliveTime,
	}
}

func (cm *ClientMgr) Add(c *websocket.Conn) {
	cm.mux.Lock()
	defer cm.mux.Unlock()
	cm.clients.Store(c, struct{}{})
	// cm.clients[c] = struct{}{}
}

func (cm *ClientMgr) Delete(c *websocket.Conn) {
	cm.mux.Lock()
	defer cm.mux.Unlock()
	// delete(cm.clients, c)
	cm.clients.Delete(c)
}

func (cm *ClientMgr) Run() {
	ticker := time.NewTicker(cm.keepaliveTime)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				cm.mux.Lock()
				defer cm.mux.Unlock()
				for wsConn := range cm.clients {
					wsConn.WriteMessage(websocket.PingMessage, nil)
				}
				fmt.Printf("keepalive: ping %v clients\n", len(cm.clients))
			}()
		case <-cm.chStop:
			return
		}
	}
}

func (cm *ClientMgr) Stop() {
	close(cm.chStop)
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.NewUpgrader()
	upgrader.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		c.WriteMessage(messageType, data)

		// update read deadline
		c.SetReadDeadline(time.Now().Add(KeepaliveTimeout))
	})
	upgrader.SetPongHandler(func(c *websocket.Conn, s string) {
		// update read deadline
		c.SetReadDeadline(time.Now().Add(KeepaliveTimeout))
	})

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	wsConn := conn.(*websocket.Conn)

	// init read deadline
	wsConn.SetReadDeadline(time.Now().Add(KeepaliveTimeout))

	clientMgr.Add(wsConn)
	wsConn.OnClose(func(c *websocket.Conn, err error) {
		clientMgr.Delete(c)
	})
}

func main() {
	clientMgr = NewClientMgr(KeepaliveTime)
	go clientMgr.Run()
	defer clientMgr.Stop()

	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)

	svr := nbhttp.NewServer(nbhttp.Config{
		Network: "tcp",
		Addrs:   []string{"localhost:8888"},
	}, mux, nil)

	err := svr.Start()
	if err != nil {
		fmt.Printf("nbio.Start failed: %v\n", err)
		return
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	svr.Shutdown(ctx)
}
