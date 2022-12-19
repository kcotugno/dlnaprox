# dlnaprox

This program is a DLNA proxy. It proxies UDP multicast packets to and from the
specified networks. These two networks are knowns as the inner and the outer
network. The basic assumption is that the inner network can send normal IP
requests to the outer network, but not visa versa. For example, a Docker
container on a bridged docker network can only be accessed through forwarded
ports. However, if it sends a request to an address on the same network as the
host system, by default, the other host is reachable. This program forwards
multicast broadcasts to 239.255.255.250:1900 to the other network. Since the
spec says that responses to the broadcast should sent to port that originated
it. So we keep a map of sockets so we can correctly proxy these responses.

## Usage

```bash
dlnaprox -i 172.17.0.0/16 -o 192.168.0.0/24
```