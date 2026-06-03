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

	if pm.DebugMode || tr.DebugMode {
		openDebugConsole()
	}

	serve(pm.Server, pm.Name)
	serve(tr.Server, tr.Name)

	go openWhenReady(pm.URL, pm.Port)
	go openWhenReady(tr.URL, tr.Port)

	systray.SetIcon(appIcon())
	systray.SetTooltip("Arx")

	mPM := systray.AddMenuItem("Open Parts Master", "Open Parts Master in browser")
	mTR := systray.AddMenuItem("Open Test Records", "Open Test Records in browser")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Arx")

	go func() {
		for {
			select {
			case <-mPM.ClickedCh:
				_ = browser.OpenURL(pm.URL)
			case <-mTR.ClickedCh:
				_ = browser.OpenURL(tr.URL)
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

func serve(s *http.Server, name string) {
	go func() {
		log.Printf("%s: starting on %s", name, s.Addr)
		if err := s.ListenAndServe(); err != nil {
			log.Printf("%s: server stopped: %v", name, err)
			systray.Quit()
		}
	}()
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
