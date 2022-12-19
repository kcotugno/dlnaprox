package internal

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

var connId = atomic.Uint64{}

type Conn struct {
	ID     uint64
	socket *net.UDPConn
	closed atomic.Bool
	Iface  net.Interface
	Src    *net.UDPAddr
	Dst    *net.UDPAddr

	Timeout time.Time

	mu *sync.Mutex

	out chan<- Message

	proxy *Proxy
}

func NewConn(p *Proxy, iface net.Interface, localIP net.IP, src *net.UDPAddr, out chan<- Message) (c *Conn, err error) {
	c = &Conn{
		ID:      connId.Add(1),
		Src:     src,
		Iface:   iface,
		Timeout: time.Now().Add(10 * time.Second),
		mu:      &sync.Mutex{},
		out:     out,
		proxy:   p,
	}

	c.socket, err = net.ListenUDP("udp4", &net.UDPAddr{IP: localIP})
	if err != nil {
		return
	}

	log.Debug().
		Str("src", c.Src.String()).
		Str("dst", c.Dst.String()).
		Msg("Opened socket")

	go c.Read()
	return
}

func (c *Conn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() {
		return
	}

	c.closed.Store(true)

	if c.socket == nil {
		return
	}

	if err := c.socket.Close(); err != nil {
		log.Err(err).Msg("Failed closing socket")
	}

	log.Debug().
		Str("src", c.Src.String()).
		Str("dst", c.Dst.String()).
		Msg("Closed socket")
}

func (c *Conn) Send(msg Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() {
		return
	}
	msg.Log(log.Info()).
		Msg("Writing packet")

	wrote, err := c.socket.WriteTo(msg.Data, msg.Dst)
	if err == nil && wrote != msg.Len {
		msg.Log(log.Error()).
			Int("wrote", wrote).
			Str("interface", c.Iface.Name).
			Msg("Failed to write full packet")
	} else if err != nil {
		msg.Log(log.Err(err)).
			Str("interface", c.Iface.Name).
			Msg("Failed to write packet")
	}

	return
}

func (c *Conn) Read() {
	defer log.Debug().
		Uint64("conn_id", c.ID).
		Str("src", c.Src.String()).
		Str("dst", c.Dst.String()).
		Msg("Connection read closed")

	var buf [4096]byte
	for !c.closed.Load() {
		read, src, err := c.socket.ReadFrom(buf[:])
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Err(err).
					Msg("Failed to read from socket")
				c.Close()
			}
			break
		}

		msg, err := NewMessage(buf[:read], src, c.Src)
		if err != nil {
			log.Err(err).
				Msg("Failed to parse message")
			continue
		}

		if c.Iface.Index == c.proxy.inner.iface.Index {
			c.proxy.rewritePacket(&msg, c.Iface)
		}

		msg.Log(log.Debug()).
			Msg("Read from socket")

		msg.Log(log.Trace()).
			Str("side", "receive").
			Str("message", string(msg.Data)).
			Send()

		c.out <- msg
	}
}

func (c *Conn) LocalAddrPort() net.Addr {
	return c.socket.LocalAddr()
}
