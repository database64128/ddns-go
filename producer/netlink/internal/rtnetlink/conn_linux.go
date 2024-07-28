package rtnetlink

import (
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Conn is a netlink connection.
type Conn struct {
	file    *os.File
	rawConn syscall.RawConn
}

// Open opens a netlink connection.
func Open(groups uint32) (*Conn, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, os.NewSyscallError("socket", err)
	}

	if err = setupSocket(fd, groups); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	f := os.NewFile(uintptr(fd), "netlink")

	rawConn, err := f.SyscallConn()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	return &Conn{
		file:    f,
		rawConn: rawConn,
	}, nil
}

// Close closes the netlink connection.
func (c *Conn) Close() error {
	return c.file.Close()
}

// SetDeadline sets the read and write deadlines associated with the connection.
func (c *Conn) SetDeadline(t time.Time) error {
	return c.file.SetDeadline(t)
}

// SetReadDeadline sets the read deadline associated with the connection.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.file.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline associated with the connection.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.file.SetWriteDeadline(t)
}

// NewRConn returns the connection wrapped in a new [RConn] for reading.
func (c *Conn) NewRConn() *RConn {
	rc := &RConn{
		Conn: *c,
	}

	rc.rawReadFunc = func(fd uintptr) (done bool) {
		var errno syscall.Errno
		rc.readN, errno = recvmsg(int(fd), rc.readMsg, rc.readFlags)
		switch errno {
		case 0:
			rc.readErr = nil
		case syscall.EAGAIN:
			return false
		default:
			rc.readErr = os.NewSyscallError("recvmsg", errno)
		}
		return true
	}

	return rc
}

// NewWConn returns the connection wrapped in a new [WConn] for writing.
func (c *Conn) NewWConn() *WConn {
	wc := &WConn{
		Conn: *c,
	}

	wc.rawWriteFunc = func(fd uintptr) (done bool) {
		var errno syscall.Errno
		wc.writeN, errno = sendmsg(int(fd), wc.writeMsg, wc.writeFlags)
		switch errno {
		case 0:
			wc.writeErr = nil
		case syscall.EAGAIN:
			return false
		default:
			wc.writeErr = os.NewSyscallError("sendmsg", errno)
		}
		return true
	}

	return wc
}

// RConn provides read access to the netlink connection.
//
// RConn is not safe for concurrent use.
// Always create a new RConn for each goroutine.
type RConn struct {
	Conn
	rawReadFunc func(fd uintptr) (done bool)
	readMsg     *unix.Msghdr
	readFlags   int
	readN       int
	readErr     error
}

// ReadMsg reads a netlink message.
func (rc *RConn) ReadMsg(msg *unix.Msghdr, flags int) (n int, err error) {
	rc.readMsg = msg
	rc.readFlags = flags
	if err = rc.rawConn.Read(rc.rawReadFunc); err != nil {
		return 0, err
	}
	return rc.readN, rc.readErr
}

// WConn provides write access to the netlink connection.
//
// WConn is not safe for concurrent use.
// Always create a new WConn for each goroutine.
type WConn struct {
	Conn
	rawWriteFunc func(fd uintptr) (done bool)
	writeMsg     *unix.Msghdr
	writeFlags   int
	writeN       int
	writeErr     error
}

// WriteMsg writes a netlink message.
func (wc *WConn) WriteMsg(msg *unix.Msghdr, flags int) (n int, err error) {
	wc.writeMsg = msg
	wc.writeFlags = flags
	if err = wc.rawConn.Write(wc.rawWriteFunc); err != nil {
		return 0, err
	}
	return wc.writeN, wc.writeErr
}

func setupSocket(fd int, groups uint32) error {
	// Set send buffer size to 32 KiB.
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 32*1024); err != nil {
		return os.NewSyscallError("setsockopt(SO_SNDBUF)", err)
	}

	// Set receive buffer size to 1 MiB.
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 1024*1024); err != nil {
		return os.NewSyscallError("setsockopt(SO_RCVBUF)", err)
	}

	// Set NETLINK_CAP_ACK to disable request echoing in ACK messages.
	if err := unix.SetsockoptInt(fd, unix.SOL_NETLINK, unix.NETLINK_CAP_ACK, 1); err != nil {
		return os.NewSyscallError("setsockopt(NETLINK_CAP_ACK)", err)
	}

	// Set NETLINK_EXT_ACK to receive extended ACK messages.
	if err := unix.SetsockoptInt(fd, unix.SOL_NETLINK, unix.NETLINK_EXT_ACK, 1); err != nil {
		return os.NewSyscallError("setsockopt(NETLINK_EXT_ACK)", err)
	}

	// Set NETLINK_GET_STRICT_CHK to enable strict input checking.
	if err := unix.SetsockoptInt(fd, unix.SOL_NETLINK, unix.NETLINK_GET_STRICT_CHK, 1); err != nil {
		return os.NewSyscallError("setsockopt(NETLINK_GET_STRICT_CHK)", err)
	}

	// Bind the socket to the netlink family and relevant groups.
	if errno := bind(fd, &unix.RawSockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: groups,
	}); errno != 0 {
		return os.NewSyscallError("bind", errno)
	}

	return nil
}

func bind(fd int, sa *unix.RawSockaddrNetlink) syscall.Errno {
	_, _, errno := unix.Syscall(unix.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(sa)), unix.SizeofSockaddrNetlink)
	return errno
}

func msgSyscall(trap uintptr, fd int, msg *unix.Msghdr, flags int) (int, syscall.Errno) {
	ret, _, errno := unix.Syscall(trap, uintptr(fd), uintptr(unsafe.Pointer(msg)), uintptr(flags))
	return int(ret), errno
}

func recvmsg(fd int, msg *unix.Msghdr, flags int) (int, syscall.Errno) {
	return msgSyscall(unix.SYS_RECVMSG, fd, msg, flags)
}

func sendmsg(fd int, msg *unix.Msghdr, flags int) (int, syscall.Errno) {
	return msgSyscall(unix.SYS_SENDMSG, fd, msg, flags)
}
