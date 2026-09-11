package lldp

import (
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Listen waits on every named interface at once for a neighbour to announce
// itself, and returns what it heard. Interfaces that say nothing within the
// wait are simply absent from the result: an unplugged port, a switch with
// LLDP turned off and a switch that has not reached its next advertisement
// are indistinguishable from here, and none of them is an error.
func Listen(interfaces []string, wait time.Duration) map[string]Neighbour {
	out := make(map[string]Neighbour, len(interfaces))
	if wait <= 0 || len(interfaces) == 0 {
		return out
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	deadline := time.Now().Add(wait)

	for _, name := range interfaces {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			n, ok := listenOne(name, deadline)
			if !ok {
				return
			}
			mu.Lock()
			out[name] = n
			mu.Unlock()
		}(name)
	}
	wg.Wait()

	return out
}

func listenOne(name string, deadline time.Time) (Neighbour, bool) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return Neighbour{}, false
	}

	// A raw packet socket bound to the LLDP protocol delivers these frames and
	// nothing else, so there is no filtering to do and no traffic to disturb.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htons(EtherType)))
	if err != nil {
		return Neighbour{}, false
	}
	defer unix.Close(fd)

	if err := unix.Bind(fd, &unix.SockaddrLinklayer{
		Protocol: htons(EtherType),
		Ifindex:  iface.Index,
	}); err != nil {
		return Neighbour{}, false
	}

	buf := make([]byte, 1600)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Neighbour{}, false
		}
		// The timeout is reset each pass so the socket never blocks past the
		// deadline, however many frames of other kinds arrive first.
		tv := unix.NsecToTimeval(int64(remaining))
		if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
			return Neighbour{}, false
		}

		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
				continue
			}
			return Neighbour{}, false
		}
		// Skip the ethernet header: destination, source, ether type.
		if n <= 14 {
			continue
		}
		if neighbour, ok := Parse(buf[14:n]); ok {
			return neighbour, true
		}
	}
}

// htons puts a port or protocol number into network byte order, which is what
// a packet socket expects even on a little endian machine.
func htons(v uint16) uint16 {
	return v<<8 | v>>8
}
