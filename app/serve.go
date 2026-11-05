package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/senneco/duet/core"
	"github.com/senneco/duet/server"
)

// Serve — само ядро: держит папку данных, следит за диском и отвечает клиентам.
// Запускается в фоне через Ensure. Если ядро для этой папки уже работает, сразу выходит.
func (cfg Config) Serve() error {
	if err := os.MkdirAll(cfg.Home, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(cfg.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Старое ядро может ещё останавливаться — немного ждём.
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if time.Now().After(deadline) {
			log.Printf("another duet is running for %s", cfg.Home)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	c, err := core.Open(cfg.Home)
	if err != nil {
		return err
	}
	stopWatch := c.Watch(time.Second)
	defer stopWatch()

	lns, port, err := listen(cfg.Port)
	if err != nil {
		return err
	}
	if port != cfg.Port {
		log.Printf("port %d is busy, using %d", cfg.Port, port)
	}
	srv := server.New(c, Build())
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	stop := sync.OnceFunc(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(ctx)
		close(done)
	})
	srv.OnShutdown = stop
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() { <-sig; stop() }()

	in := info{PID: os.Getpid(), Port: port, Build: Build()}
	if err := writeInfo(cfg.infoPath(), in); err != nil {
		return err
	}
	for _, ln := range lns {
		go func() {
			if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Print(err)
				stop()
			}
		}()
	}
	log.Printf("duet %s on %s (pid %d)", in.Build, URL(port), in.PID)
	<-done
	// server.json убираем, только если он наш: новое ядро могло уже записать свой.
	if data, err := os.ReadFile(cfg.infoPath()); err == nil {
		var cur info
		if json.Unmarshal(data, &cur) == nil && cur.PID == in.PID {
			os.Remove(cfg.infoPath())
		}
	}
	log.Printf("duet stopped")
	return nil
}

func writeInfo(path string, in info) error {
	data, _ := json.Marshal(in)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// listen слушает порт на 127.0.0.1 и ::1 (браузер может пойти на любой из них).
// Порт занят другой программой — берётся свободный.
func listen(port int) ([]net.Listener, int, error) {
	if lns, err := listenBoth(port); err == nil {
		return lns, port, nil
	}
	for range 10 {
		v4, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, 0, err
		}
		p := v4.Addr().(*net.TCPAddr).Port
		v4.Close()
		if lns, err := listenBoth(p); err == nil {
			return lns, p, nil
		}
	}
	return nil, 0, errors.New("no free port")
}

func listenBoth(port int) ([]net.Listener, error) {
	v4, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	v6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			// На ::1 этот порт держит кто-то другой: браузер мог бы попасть к нему.
			v4.Close()
			return nil, err
		}
		return []net.Listener{v4}, nil // IPv6 выключен — хватит и 127.0.0.1
	}
	return []net.Listener{v4, v6}, nil
}
