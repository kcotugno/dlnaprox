package internal

import (
	"fmt"
	"hash/crc32"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

var messageID = atomic.Uint64{}

type Message struct {
	ID   uint64
	Data []byte
	Len  int

	Src *net.UDPAddr
	Dst *net.UDPAddr

	Checksum  string
	Timestamp time.Time
}

func NewMessage(data []byte, src net.Addr, dst net.Addr) (msg Message, err error) {
	msg.SetData(data)

	var addr netip.AddrPort
	addr, err = netip.ParseAddrPort(src.String())
	if err != nil {
		return
	}
	msg.Src = net.UDPAddrFromAddrPort(addr)

	addr, err = netip.ParseAddrPort(dst.String())
	if err != nil {
		return
	}
	msg.Dst = net.UDPAddrFromAddrPort(addr)
	msg.Timestamp = time.Now()
	msg.ID = messageID.Add(1)

	return
}

func (m Message) Log(event *zerolog.Event) *zerolog.Event {
	return event.
		Uint64("id", m.ID).
		Int("length", m.Len).
		Str("src", m.Src.String()).
		Str("dst", m.Dst.String()).
		Str("checksum", m.Checksum)
}

func (m *Message) SetData(data []byte) {
	m.Data = make([]byte, len(data))
	copy(m.Data, data)
	m.Len = len(m.Data)
	m.Checksum = fmt.Sprintf("%04X", crc32.ChecksumIEEE(m.Data))
}
