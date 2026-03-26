//go:build windows

package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procCreatePseudoConsole    = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole    = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole     = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAL = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttr   = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAL     = kernel32.NewProc("DeleteProcThreadAttributeList")
)

const _PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE = 0x00020016

// conPtyHandle wraps a Windows ConPTY with I/O pipes.
type conPtyHandle struct {
	inPipe  *os.File       // write to this to send input to the process
	outPipe *os.File       // read from this to get process output
	hPC     windows.Handle // pseudo console handle
}

func (h *conPtyHandle) Read(p []byte) (int, error) {
	return h.outPipe.Read(p)
}

func (h *conPtyHandle) Write(p []byte) (int, error) {
	return h.inPipe.Write(p)
}

func (h *conPtyHandle) Close() error {
	var firstErr error
	if h.inPipe != nil {
		if err := h.inPipe.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if h.outPipe != nil {
		if err := h.outPipe.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if h.hPC != 0 {
		procClosePseudoConsole.Call(uintptr(h.hPC))
		h.hPC = 0
	}
	return firstErr
}

func (h *conPtyHandle) SetSize(rows, cols uint16) error {
	// COORD is packed as two int16 values: X (cols), Y (rows).
	coord := uintptr(cols) | (uintptr(rows) << 16)
	ret, _, err := procResizePseudoConsole.Call(uintptr(h.hPC), coord)
	if ret != 0 {
		return fmt.Errorf("ResizePseudoConsole failed: %w", err)
	}
	return nil
}

// windowsPty creates pseudo-terminals using Windows ConPTY.
type windowsPty struct{}

func (p *windowsPty) Start(cmd *exec.Cmd) (PtyHandle, error) {
	// Create pipes for ConPTY I/O.
	var ptyInRead, ptyInWrite, ptyOutRead, ptyOutWrite windows.Handle
	if err := windows.CreatePipe(&ptyInRead, &ptyInWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe (stdin): %w", err)
	}
	if err := windows.CreatePipe(&ptyOutRead, &ptyOutWrite, nil, 0); err != nil {
		windows.CloseHandle(ptyInRead)
		windows.CloseHandle(ptyInWrite)
		return nil, fmt.Errorf("CreatePipe (stdout): %w", err)
	}

	// Create the pseudo console with a default 80x24 size.
	coord := uintptr(80) | (uintptr(24) << 16) // COORD{X:80, Y:24}
	var hPC windows.Handle
	ret, _, err := procCreatePseudoConsole.Call(
		coord,
		uintptr(ptyInRead),
		uintptr(ptyOutWrite),
		0,
		uintptr(unsafe.Pointer(&hPC)),
	)
	if ret != 0 {
		windows.CloseHandle(ptyInRead)
		windows.CloseHandle(ptyInWrite)
		windows.CloseHandle(ptyOutRead)
		windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("CreatePseudoConsole failed: %w", err)
	}

	// ConPTY now owns these pipe ends; close our copies.
	windows.CloseHandle(ptyInRead)
	windows.CloseHandle(ptyOutWrite)

	// Set up the process with the pseudo console attribute.
	if err := startProcessWithConPty(cmd, hPC); err != nil {
		windows.CloseHandle(ptyInWrite)
		windows.CloseHandle(ptyOutRead)
		procClosePseudoConsole.Call(uintptr(hPC))
		return nil, err
	}

	return &conPtyHandle{
		inPipe:  os.NewFile(uintptr(ptyInWrite), "conpty-in"),
		outPipe: os.NewFile(uintptr(ptyOutRead), "conpty-out"),
		hPC:     hPC,
	}, nil
}

func (p *windowsPty) Close() {}

// MakePtyFactory returns a PtyFactory appropriate for the current platform.
func MakePtyFactory() PtyFactory {
	return &windowsPty{}
}

// startProcessWithConPty starts a process attached to the given ConPTY handle.
func startProcessWithConPty(cmd *exec.Cmd, hPC windows.Handle) error {
	// Determine attribute list size.
	var attrListSize uintptr
	procInitializeProcThreadAL.Call(0, 1, 0, 0, uintptr(unsafe.Pointer(&attrListSize)))
	if attrListSize == 0 {
		return fmt.Errorf("InitializeProcThreadAttributeList returned zero size")
	}

	attrList := make([]byte, attrListSize)
	attrListPtr := unsafe.Pointer(&attrList[0])

	ret, _, err := procInitializeProcThreadAL.Call(
		uintptr(attrListPtr), 1, 0, uintptr(unsafe.Pointer(&attrListSize)),
	)
	if ret == 0 {
		return fmt.Errorf("InitializeProcThreadAttributeList: %w", err)
	}
	defer procDeleteProcThreadAL.Call(uintptr(attrListPtr))

	// Add the pseudo console attribute.
	ret, _, err = procUpdateProcThreadAttr.Call(
		uintptr(attrListPtr),
		0,
		_PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(hPC),
		unsafe.Sizeof(hPC),
		0, 0,
	)
	if ret == 0 {
		return fmt.Errorf("UpdateProcThreadAttribute: %w", err)
	}

	// Build the command line string.
	cmdLine := cmd.Path
	if len(cmd.Args) > 1 {
		cmdLine = windows.ComposeCommandLine(cmd.Args)
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return err
	}

	var dirPtr *uint16
	dir := cmd.Dir
	if dir != "" {
		dirPtr, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return err
		}
	}

	// Create the process with extended startup info.
	si := &windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.ProcThreadAttributeList = (*windows.ProcThreadAttributeList)(attrListPtr)

	var pi windows.ProcessInformation
	createFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT)

	err = windows.CreateProcess(
		nil,
		cmdLinePtr,
		nil, nil,
		false,
		createFlags,
		nil, // inherit environment
		dirPtr,
		&si.StartupInfo,
		&pi,
	)
	if err != nil {
		return fmt.Errorf("CreateProcess: %w", err)
	}

	// Close the thread handle; we don't need it.
	windows.CloseHandle(pi.Thread)
	windows.CloseHandle(pi.Process)

	// Store the process for cmd if possible (for wait/kill support).
	if cmd.Process == nil {
		cmd.Process, _ = os.FindProcess(int(pi.ProcessId))
	}

	return nil
}
