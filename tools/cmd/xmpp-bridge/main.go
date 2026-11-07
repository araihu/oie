// xmpp-bridge forwards local TCP to a remote Docker host for development.
// It copies bytes without parsing XMPP or terminating its TLS connection.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	address := flag.String("listen", "127.0.0.1:5222", "local XMPP listen address")
	target := flag.String("target", "host.docker.internal:5222", "remote Docker host XMPP address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	go func() { <-ctx.Done(); listener.Close() }()
	log.Printf("development TCP bridge listening at %s, target %s", listener.Addr(), *target)
	var workers sync.WaitGroup
	limit := make(chan struct{}, 16)
	for {
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("bridge accept: %v", err)
			}
			break
		}
		select {
		case limit <- struct{}{}:
		default:
			client.Close()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-limit }()
			defer client.Close()
			server, err := (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", *target)
			if err != nil {
				log.Printf("development bridge target unavailable: %v", err)
				return
			}
			defer server.Close()
			closeOnStop := context.AfterFunc(ctx, func() { client.Close(); server.Close() })
			defer closeOnStop()
			uploaded := make(chan struct{})
			go func() { io.Copy(server, client); server.Close(); close(uploaded) }()
			io.Copy(client, server)
			client.Close()
			<-uploaded
		}()
	}
	workers.Wait()
}
