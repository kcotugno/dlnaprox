package internal

import (
	"net"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type ConnTable struct {
	idTable  map[uint64]*Conn
	srcTable map[string]*Conn
	dstTable map[string]*Conn
	mu       *sync.Mutex
	out      chan<- Message
	proxy    *Proxy
}

func NewConnTable(p *Proxy, out chan<- Message) *ConnTable {
	return &ConnTable{
		idTable:  make(map[uint64]*Conn),
		srcTable: make(map[string]*Conn),
		dstTable: make(map[string]*Conn),
		mu:       &sync.Mutex{},
		out:      out,
		proxy:    p,
	}
}

func (c *ConnTable) Get(iface net.Interface, localIP net.IP, src, dst *net.UDPAddr) (*Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Trace().Str("dst", dst.String()).Msg("Getting connection")

	srcConn, srcOk := c.srcTable[src.String()]
	dstConn, dstOk := c.dstTable[dst.String()]

	if srcOk {
		return srcConn, nil
	} else if dstOk {
		return dstConn, nil
	}

	ip := make(net.IP, 4)
	copy(ip, localIP.To4())
	conn, err := NewConn(c.proxy, iface, ip, src, c.out)
	if err != nil {
		return nil, err
	}
	c.idTable[conn.ID] = conn
	c.srcTable[conn.Src.String()] = conn
	c.dstTable[conn.Dst.String()] = conn
	go c.watchForTimeout(conn)

	return conn, nil
}

func (c *ConnTable) watchForTimeout(conn *Conn) {
	var shouldClose bool
	defer func() {
		conn.Close()
		c.mu.Lock()
		defer c.mu.Unlock()

		delete(c.idTable, conn.ID)
		delete(c.srcTable, conn.Src.String())
		delete(c.dstTable, conn.Dst.String())
	}()

	for !shouldClose {
		conn.mu.Lock()
		if conn.Timeout.Before(time.Now()) || conn.closed.Load() {
			shouldClose = true
			log.Debug().
				Uint64("conn_id", conn.ID).
				Msg("Connection timed-out")
		}
		conn.mu.Unlock()

		if !shouldClose {
			time.Sleep(conn.Timeout.Sub(time.Now()))
		}
	}
}
