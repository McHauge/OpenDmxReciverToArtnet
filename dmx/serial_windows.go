//go:build windows

package dmx

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	dmxByteSize = 8
	dmxStopBits = 2 // TWOSTOPBITS
	dmxParity   = 0 // NOPARITY

	// ClearCommError status bits.
	ceRxOver    = 0x0001
	ceOverrun   = 0x0002
	ceRxParity  = 0x0004
	ceFrame     = 0x0008
	ceBreak     = 0x0010
	ceErrorMask = ceRxOver | ceOverrun | ceRxParity | ceFrame

	purgeRxClear = 0x0008

	// Read timeout: 2ms between bytes signals end of frame
	readIntervalTimeout   = 2
	readTotalTimeoutMult  = 0
	readTotalTimeoutConst = 100
)

type SerialPort struct {
	handle  windows.Handle
	overlap windows.Overlapped
	mu      sync.Mutex
}

func OpenSerialPort(portName string) (*SerialPort, error) {
	path, err := windows.UTF16PtrFromString(resolvePortPath(portName))
	if err != nil {
		return nil, fmt.Errorf("invalid port name: %w", err)
	}

	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", portName, err)
	}

	sp := &SerialPort{handle: handle}

	if err := sp.configure(); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}

	// Create event for overlapped I/O
	evt, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("CreateEvent: %w", err)
	}
	sp.overlap.HEvent = evt

	// Purge any stale data
	purgeComm(handle, purgeRxClear)

	return sp, nil
}

func (sp *SerialPort) configure() error {
	var dcb dcbStruct
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))

	if err := getCommState(sp.handle, &dcb); err != nil {
		return fmt.Errorf("GetCommState: %w", err)
	}

	dcb.BaudRate = dmxBaudRate
	dcb.ByteSize = dmxByteSize
	dcb.StopBits = dmxStopBits
	dcb.Parity = dmxParity
	dcb.Flags = 0x0001 // fBinary = 1, everything else off

	if err := setCommState(sp.handle, &dcb); err != nil {
		return fmt.Errorf("SetCommState: %w", err)
	}

	// Set timeouts for read operations
	timeouts := commTimeouts{
		ReadIntervalTimeout:        readIntervalTimeout,
		ReadTotalTimeoutMultiplier: readTotalTimeoutMult,
		ReadTotalTimeoutConstant:   readTotalTimeoutConst,
	}
	if err := setCommTimeouts(sp.handle, &timeouts); err != nil {
		return fmt.Errorf("SetCommTimeouts: %w", err)
	}

	return nil
}

func (sp *SerialPort) Read(buf []byte) (int, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	var n uint32
	err := windows.ReadFile(sp.handle, buf, &n, &sp.overlap)
	if err == windows.ERROR_IO_PENDING {
		err = windows.GetOverlappedResult(sp.handle, &sp.overlap, &n, true)
	}
	if err != nil {
		return int(n), err
	}
	return int(n), nil
}

// ReadChunk implements Port.
//
// The break flag is sampled with ClearCommError immediately after the read, on
// the same goroutine, so it is reported against the bytes it actually followed.
// The previous design signalled breaks from a separate WaitCommEvent goroutine,
// where the signal could overtake the data ahead of it and truncate a frame.
//
// This relies on the read terminating at the break, which FTDI adapters give us
// for free: the chip sends a USB packet immediately on any line-status change
// (BI/FE/PE). Without that, nothing here would be break-aligned — at 44fps
// there is never a 2ms inter-byte gap for ReadIntervalTimeout to fire on.
func (sp *SerialPort) ReadChunk(buf []byte) (int, Marker, error) {
	n, err := sp.Read(buf)
	if err != nil {
		return n, MarkerNone, err
	}

	var status uint32
	var stat comStat
	if err := clearCommError(sp.handle, &status, &stat); err != nil {
		// Not fatal: we still have the data, just no line-event information.
		return n, MarkerNone, nil
	}

	switch {
	case status&ceBreak != 0:
		return n, MarkerBreak, nil
	case status&ceErrorMask != 0:
		return n, MarkerError, nil
	}
	return n, MarkerNone, nil
}

func (sp *SerialPort) Close() error {
	windows.CloseHandle(sp.overlap.HEvent)
	return windows.CloseHandle(sp.handle)
}

// Win32 structures and syscalls

type dcbStruct struct {
	DCBlength uint32
	BaudRate  uint32
	Flags     uint32
	Reserved  uint16
	XonLim    uint16
	XoffLim   uint16
	ByteSize  byte
	Parity    byte
	StopBits  byte
	XonChar   byte
	XoffChar  byte
	ErrorChar byte
	EofChar   byte
	EvtChar   byte
	Reserved1 uint16
}

type commTimeouts struct {
	ReadIntervalTimeout         uint32
	ReadTotalTimeoutMultiplier  uint32
	ReadTotalTimeoutConstant    uint32
	WriteTotalTimeoutMultiplier uint32
	WriteTotalTimeoutConstant   uint32
}

type comStat struct {
	Flags  uint32
	InQue  uint32
	OutQue uint32
}

var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procGetCommState    = kernel32.NewProc("GetCommState")
	procSetCommState    = kernel32.NewProc("SetCommState")
	procSetCommTimeouts = kernel32.NewProc("SetCommTimeouts")
	procClearCommError  = kernel32.NewProc("ClearCommError")
	procPurgeComm       = kernel32.NewProc("PurgeComm")
)

func getCommState(handle windows.Handle, dcb *dcbStruct) error {
	r, _, err := procGetCommState.Call(uintptr(handle), uintptr(unsafe.Pointer(dcb)))
	if r == 0 {
		return err
	}
	return nil
}

func setCommState(handle windows.Handle, dcb *dcbStruct) error {
	r, _, err := procSetCommState.Call(uintptr(handle), uintptr(unsafe.Pointer(dcb)))
	if r == 0 {
		return err
	}
	return nil
}

func setCommTimeouts(handle windows.Handle, timeouts *commTimeouts) error {
	r, _, err := procSetCommTimeouts.Call(uintptr(handle), uintptr(unsafe.Pointer(timeouts)))
	if r == 0 {
		return err
	}
	return nil
}

func clearCommError(handle windows.Handle, errors *uint32, stat *comStat) error {
	r, _, err := procClearCommError.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(errors)),
		uintptr(unsafe.Pointer(stat)),
	)
	if r == 0 {
		return err
	}
	return nil
}

func purgeComm(handle windows.Handle, flags uint32) error {
	r, _, err := procPurgeComm.Call(uintptr(handle), uintptr(flags))
	if r == 0 {
		return err
	}
	return nil
}
