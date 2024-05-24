package vmess

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/metacubex/mihomo/log"

	C "github.com/metacubex/mihomo/constant"
)

var cw2Initialize = sync.Once{}
var cw2HttpClient *http.Client

type CW2Config struct {
	Up        string
	Down      string
	TLS       bool
	TLSConfig *tls.Config
}

func StreamCW2Conn(cfg *CW2Config, dialer C.Dialer) (net.Conn, error) {
	cw2Initialize.Do(func() {
		cw2HttpClient = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.DialContext(ctx, network, addr)
				},
			},
		}
	})

	// pipe
	conn1, conn2 := net.Pipe()

	// generate session id
	sessionId := make([]byte, 8)
	_, err := rand.Read(sessionId)
	if err != nil {
		panic(err)
	}
	sessionIdHex := hex.EncodeToString(sessionId)

	ctx, cancel := context.WithCancel(context.Background())

	upRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Up, conn2)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cw2: %w", err)
	}
	upRequest.Header.Add("X-Session-Id", sessionIdHex)

	downRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Down, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cw2: %w", err)
	}
	downRequest.Header.Add("X-Session-Id", sessionIdHex)

	// up
	go func() {
		defer cancel()

		resp, err := cw2HttpClient.Do(upRequest)
		if err != nil {
			unwrap := errors.Unwrap(err)
			// ignore the error if it is caused by context cancel
			if !(unwrap != nil && errors.Is(unwrap, context.Canceled)) {
				log.Errorln("cw2 upload: %s", err)
			}
			return
		}

		defer resp.Body.Close()

		io.Copy(io.Discard, resp.Body)
	}()

	// down
	go func() {
		defer cancel()

		resp, err := cw2HttpClient.Do(downRequest)
		if err != nil {
			unwrap := errors.Unwrap(err)
			// ignore the error if it is caused by context cancel
			if !(unwrap != nil && errors.Is(unwrap, context.Canceled)) {
				log.Errorln("cw2 download: %s", err)
			}
			return
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Errorln("cw2 download: %s", resp.Status)
			return
		}

		io.Copy(conn2, resp.Body)
	}()

	go func() {
		<-ctx.Done()
		conn2.Close()
	}()

	return conn1, nil
}
