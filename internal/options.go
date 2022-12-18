package internal

import "net"

type Options struct {
	OuterIP      net.IP
	InnerNetwork net.IPNet
	OuterNetwork net.IPNet
	Rewrite      bool
	Replacement  string
	MatchPrefix  string
	MatchPostfix string
}
