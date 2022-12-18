package internal

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

const ipRegexSrc = `[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}`

var NoInterfaceFound = errors.New("Couldn't find interface for network")
var InnerNetworkNotMulticast = errors.New("Inner network interface is not multicast")
var OuterNetworkNotMulticast = errors.New("Outer network interface is not multicast")

var multicastAddr = net.IPv4(239, 255, 255, 250)

var ipRegex = regexp.MustCompile(ipRegexSrc)

type netGroup struct {
	iface net.Interface
	ip    net.IP
	net   *net.IPNet
}

type Proxy struct {
	inner netGroup
	outer netGroup

	matchRegex *regexp.Regexp

	opts Options
}

func NewProxy(opts Options) (proxy Proxy, err error) {
	if opts.InnerNetwork.Contains(opts.OuterNetwork.IP) || opts.OuterNetwork.Contains(opts.InnerNetwork.IP) {
		err = errors.New("Networks cannot overlap")
		return
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}

	for _, iface := range interfaces {
		var addrs []net.Addr
		addrs, err = iface.Addrs()
		if err != nil {
			return
		}

		for _, addr := range addrs {
			var ip net.IP
			var ipNet *net.IPNet
			ip, ipNet, err = net.ParseCIDR(addr.String())
			if err != nil {
				return
			}

			if opts.InnerNetwork.IP.Equal(ipNet.IP) && opts.InnerNetwork.Mask.String() == ipNet.Mask.String() {
				log.Debug().
					IPAddr("ip", ip).
					Str("interface", iface.Name).
					Msg("found inner network interface")

				proxy.inner = netGroup{
					iface: iface,
					ip:    ip,
					net:   ipNet,
				}
				break
			} else if opts.OuterIP.Equal(ip) || (opts.OuterIP.IsUnspecified() && opts.OuterNetwork.IP.Equal(ipNet.IP) && opts.OuterNetwork.Mask.String() == ipNet.Mask.String()) {
				log.Debug().
					IPAddr("ip", ip).
					Str("interface", iface.Name).
					Msg("found outer network interface")

				proxy.outer = netGroup{
					iface: iface,
					ip:    ip,
					net:   ipNet,
				}
				break
			}
		}
	}

	if proxy.inner.iface.Index == 0 || proxy.outer.iface.Index == 0 {
		err = NoInterfaceFound
		return
	}

	if (proxy.inner.iface.Flags & net.FlagMulticast) == 0 {
		err = InnerNetworkNotMulticast
		return
	} else if (proxy.outer.iface.Flags & net.FlagMulticast) == 0 {
		err = OuterNetworkNotMulticast
		return
	}

	proxy.opts = opts
	err = proxy.buildRegex()
	return
}

func (p Proxy) Run() {
	var wg sync.WaitGroup

	chan1 := make(chan Message, 16)
	chan2 := make(chan Message, 16)

	go func() {
		for {
			log.Debug().Int("goroutines", runtime.NumGoroutine()).Send()
			time.Sleep(3 * time.Second)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		p.handleInterface(p.inner, chan1, chan2, true)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		p.handleInterface(p.outer, chan2, chan1, false)
	}()

	wg.Wait()
}

func (p *Proxy) handleInterface(group netGroup, in <-chan Message, out chan<- Message, rewrite bool) {
	var wg sync.WaitGroup

	defer fmt.Println("done " + group.iface.Name)
	defer wg.Wait()
	defer close(out)
	defer fmt.Println("closing " + group.iface.Name)

	conn, err := newMuticastSocket(group.iface)
	if err != nil {
		log.Err(err).Msg("Failed to open multicast socker")
		os.Exit(1)
	}

	conns := NewConnTable(p, out)
	go func() {
		for {
			time.Sleep(3 * time.Second)
			conns.mu.Lock()
			log.Debug().
				Int("src_table_count", len(conns.srcTable)).
				Int("dst_table_count", len(conns.dstTable)).
				Str("interface", group.iface.Name).
				Interface("src_table", conns.srcTable).
				Interface("dst_table", conns.dstTable).
				Interface("id_table", conns.idTable).
				Send()
			conns.mu.Unlock()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		for msg := range in {
			conn, err := conns.Get(group.iface, group.ip, msg.Src, msg.Dst)
			if err != nil {
				msg.Log(log.Err(err)).
					Str("interface", group.iface.Name).
					Msg("Failed to get UDP socket")
			}
			conn.Send(msg)

			msg.Log(log.Trace()).
				Str("type", "send").
				Str("message", string(msg.Data)).
				Send()
		}
	}()

	var buf [4096]byte
	for {
		read, _, from, err := conn.ReadFrom(buf[:])
		if err != nil {
			log.Err(err).
				Str("interface", group.iface.Name).
				Msg("Failed to read from socket")
			os.Exit(1)
		}

		msg, err := NewMessage(buf[:read], from, &net.UDPAddr{IP: multicastAddr, Port: 1900})
		if err != nil {
			log.Err(err).
				Msg("Failed to parse message")
		}
		if msg.Src.IP.String() == "0.0.0.0" || msg.Dst.IP.String() == "0.0.0.0" {
			panic(msg)
		}

		event := msg.Log(log.Debug()).
			Str("interface", group.iface.Name)

		if p.inner.ip.Equal(msg.Src.IP) || p.outer.ip.Equal(msg.Src.IP) {
			event.Msg("Ignoring packet from local interface")
			continue
		}

		event.Msg("Read from socket")

		msg.Log(log.Trace()).
			Str("side", "receive").
			Str("message", string(msg.Data)).
			Send()

		if p.inner.iface.Index == group.iface.Index {
			p.rewritePacket(&msg, group.iface)
		}

		out <- msg
	}
}

func (p *Proxy) rewritePacket(msg *Message, iface net.Interface) {
	if !p.opts.Rewrite {
		return
	}

	msg.Log(log.Debug()).
		Msg("Rewriting packet")

	data := p.matchRegex.ReplaceAllFunc(msg.Data, func(match []byte) []byte {
		ipMatch := ipRegex.Find(match)

		matchIP := net.ParseIP(string(ipMatch))
		if matchIP == nil {
			msg.Log(log.Warn()).
				Str("source", msg.Src.String()).
				Str("interface", iface.Name).
				Msg("Matched IP is not a valid IPv4 address")
			return match
		}

		if matchIP.IsMulticast() {
			return match
		}

		if p.opts.InnerNetwork.Contains(matchIP) {
			msg.Log(log.Info()).
				IPAddr("match", matchIP).
				Str("source", msg.Src.String()).
				Str("interface", iface.Name).
				Msg("Rewriting IP address in message")

			if p.opts.Replacement == "" {
				return []byte(p.outer.ip.String())
			} else {
				return []byte(p.opts.Replacement)
			}
		}

		return match
	})

	msg.SetData(data)
}

func (p *Proxy) buildRegex() error {
	if p.opts.MatchPrefix == "" && p.opts.MatchPostfix == "" {
		p.matchRegex = ipRegex
	}

	r, err := regexp.Compile(p.opts.MatchPrefix + ipRegexSrc + p.opts.MatchPostfix)

	p.matchRegex = r

	return err
}

func Start(opts Options) error {
	proxy, err := NewProxy(opts)
	if err != nil {
		return err
	}

	proxy.Run()
	return nil
}

func newMuticastSocket(iface net.Interface) (*ipv4.PacketConn, error) {
	socket, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		log.Err(err).Msg("Failed to open socket")
		return nil, err
	}

	if err := unix.SetsockoptInt(socket, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		log.Err(err).Msg("Failed to set socket option")
		return nil, err
	}

	if err := unix.SetsockoptString(socket, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface.Name); err != nil {
		return nil, err
	}

	rawSockAddr := &unix.SockaddrInet4{Port: 1900}
	copy(rawSockAddr.Addr[:], []byte{239, 255, 255, 250})

	if err := unix.Bind(socket, rawSockAddr); err != nil {
		log.Err(err).
			Str("interface", iface.Name).
			Msg("Failed to set bind to interface")
		return nil, err
	}

	f := os.NewFile(uintptr(socket), "")
	listener, err := net.FilePacketConn(f)
	if err != nil {
		log.Err(err).Msg("Failed to listen on multicast address")
		return nil, err
	}
	f.Close()

	conn := ipv4.NewPacketConn(listener)

	if err := conn.JoinGroup(&iface, &net.UDPAddr{IP: multicastAddr}); err != nil {
		log.Err(err).Msg("Failed to join multicast group")
		return nil, err
	}

	if err := conn.SetMulticastLoopback(false); err != nil {
		log.Err(err).Msg("Failed to turn off multicast loopback")
		return nil, err
	}

	return conn, nil
}
