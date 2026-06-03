package main

import (
	"log"
	"os"
	"syscall"
)

var consoleOpen bool

// openDebugConsole allocates a Windows console window and redirects log output to it.
// Called at startup when DebugMode is true; requires a restart to take effect.
func openDebugConsole() {
	if consoleOpen {
		return
	}
	consoleOpen = true
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	kernel32.NewProc("AllocConsole").Call()

	conout, err := syscall.Open("CONOUT$", syscall.O_RDWR, 0)
	if err != nil {
		return
	}

	setStdHandle := kernel32.NewProc("SetStdHandle")
	const (
		stdOutputHandle = uintptr(0xFFFFFFF5) // STD_OUTPUT_HANDLE
		stdErrorHandle  = uintptr(0xFFFFFFF4) // STD_ERROR_HANDLE
	)
	setStdHandle.Call(stdOutputHandle, uintptr(conout))
	setStdHandle.Call(stdErrorHandle, uintptr(conout))

	os.Stdout = os.NewFile(uintptr(conout), "stdout")
	os.Stderr = os.NewFile(uintptr(conout), "stderr")
	log.SetOutput(os.Stdout)
}
