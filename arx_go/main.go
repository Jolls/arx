package main

import (
	"log"
	"net"
	"net/http"
	"time"

	"github.com/getlantern/systray"
	"github.com/pkg/browser"

	partsmaster "arx/parts_master_go"
	testrecords "arx/test_records_go"
)

var pm *partsmaster.App
var tr *testrecords.App

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	pm = partsmaster.New()
	tr = testrecords.New()

	// Wire TR as the fallback for paths PM doesn't match.
	pm.SetFallback(tr.Handler())

	// After settings save, reload TR so its DB pool + config stay in sync.
	pm.SetAfterSettingsSave(func() { _ = tr.Reload() })

	if pm.DebugMode || tr.DebugMode {
		openDebugConsole()
	}

	// Single HTTP server on PM's port; TR routes fall through from PM's router.
	server := &http.Server{
		Addr:    "0.0.0.0:" + pm.Port,
		Handler: pm.Handler(),
	}
	go func() {
		log.Printf("Arx: starting on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Arx: server stopped: %v", err)
			systray.Quit()
		}
	}()

	go openWhenReady(pm.URL, pm.Port)

	systray.SetIcon(appIcon())
	systray.SetTooltip("Arx")

	mOpen := systray.AddMenuItem("Open Arx", "Open Arx in browser")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Arx")

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				_ = browser.OpenURL(pm.URL)
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onExit() {
	if pm != nil {
		pm.Close()
	}
	if tr != nil {
		tr.Close()
	}
}

func openWhenReady(url, port string) {
	for i := 0; i < 40; i++ {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 50*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = browser.OpenURL(url)
}
