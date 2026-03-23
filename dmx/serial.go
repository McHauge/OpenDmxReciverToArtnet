package dmx

import (
	"context"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	dmxBaudRate = 250000
	dmxByteSize = 8
	dmxStopBits = 2 // TWOSTOPBITS
	dmxParity   = 0 // NOPARITY

	evBreak  = 0x0040
	evRxChar = 0x0001
	evErr    = 0x0080
	ceBreak  = 0x0010

	purgeRxClear = 0x0008

	// Read timeout: 2ms between bytes signals end of frame
	readIntervalTimeout  = 2
	readTotalTimeoutMult = 0
	readTotalTimeoutConst = 100
)

type SerialPort struct {
	handle  windows.Handle
	overlap windows.Overlapped
	mu      sync.Mutex
}

func OpenSerialPort(portName string) (*SerialPort, error) {
	path, err := windows.UTF16PtrFromString(`\\.\` + portName)
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
		ReadIntervalTimeout:         readIntervalTimeout,
		ReadTotalTimeoutMultiplier:  readTotalTimeoutMult,
		ReadTotalTimeoutConstant:    readTotalTimeoutConst,
	}
	if err := setCommTimeouts(sp.handle, &timeouts); err != nil {
		return fmt.Errorf("SetCommTimeouts: %w", err)
	}

	// Set comm event mask for BREAK detection
	if err := setCommMask(sp.handle, evBreak|evRxChar|evErr); err != nil {
		return fmt.Errorf("SetCommMask: %w", err)
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

// WaitForBreak blocks until a BREAK condition is detected on the serial line.
func (sp *SerialPort) WaitForBreak(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var evtMask uint32
		var ov windows.Overlapped
		evt, err := windows.CreateEvent(nil, 1, 0, nil)
		if err != nil {
			return err
		}
		ov.HEvent = evt

		err = waitCommEvent(sp.handle, &evtMask, &ov)
		if err == windows.ERROR_IO_PENDING {
			// Wait with context cancellation support
			waitResult, _ := windows.WaitForSingleObject(evt, 50) // 50ms poll
			if waitResult == uint32(windows.WAIT_TIMEOUT) {
				windows.CancelIo(sp.handle)
				windows.CloseHandle(evt)
				continue
			}
			var transferred uint32
			err = windows.GetOverlappedResult(sp.handle, &ov, &transferred, false)
		}
		windows.CloseHandle(evt)

		if err != nil {
			return err
		}

		if evtMask&evBreak != 0 {
			// Clear the error state caused by BREAK
			sp.clearBreakError()
			return nil
		}
	}
}

// CheckBreak does a non-blocking check for BREAK via ClearCommError.
func (sp *SerialPort) CheckBreak() (bool, error) {
	var errors uint32
	var stat comStat
	if err := clearCommError(sp.handle, &errors, &stat); err != nil {
		return false, err
	}
	return errors&ceBreak != 0, nil
}

func (sp *SerialPort) clearBreakError() {
	var errors uint32
	var stat comStat
	clearCommError(sp.handle, &errors, &stat)
}

func (sp *SerialPort) Close() error {
	// Unblock any pending WaitCommEvent by clearing the mask
	setCommMask(sp.handle, 0)
	windows.CloseHandle(sp.overlap.HEvent)
	return windows.CloseHandle(sp.handle)
}

// Win32 structures and syscalls

type dcbStruct struct {
	DCBlength  uint32
	BaudRate   uint32
	Flags      uint32
	Reserved   uint16
	XonLim     uint16
	XoffLim    uint16
	ByteSize   byte
	Parity     byte
	StopBits   byte
	XonChar    byte
	XoffChar   byte
	ErrorChar  byte
	EofChar    byte
	EvtChar    byte
	Reserved1  uint16
}

type commTimeouts struct {
	ReadIntervalTimeout         uint32
	ReadTotalTimeoutMultiplier  uint32
	ReadTotalTimeoutConstant    uint32
	WriteTotalTimeoutMultiplier uint32
	WriteTotalTimeoutConstant   uint32
}

type comStat struct {
	Flags    uint32
	InQue   uint32
	OutQue  uint32
}

var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procGetCommState    = kernel32.NewProc("GetCommState")
	procSetCommState    = kernel32.NewProc("SetCommState")
	procSetCommTimeouts = kernel32.NewProc("SetCommTimeouts")
	procSetCommMask     = kernel32.NewProc("SetCommMask")
	procWaitCommEvent   = kernel32.NewProc("WaitCommEvent")
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

func setCommMask(handle windows.Handle, mask uint32) error {
	r, _, err := procSetCommMask.Call(uintptr(handle), uintptr(mask))
	if r == 0 {
		return err
	}
	return nil
}

func waitCommEvent(handle windows.Handle, evtMask *uint32, overlapped *windows.Overlapped) error {
	r, _, err := procWaitCommEvent.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(evtMask)),
		uintptr(unsafe.Pointer(overlapped)),
	)
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
