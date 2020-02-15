package main

import (
	"context"
	"flag"
	"log"

	"github.com/po3rin/kvraft/server"
	"github.com/po3rin/kvraft/store"
)

func main() {
	port := flag.Int("port", 3000, "key-value server port")
	flag.Parse()

	s := store.New()
	srv := server.New(*port, s)

	// とりあえずここでは context.Background() を渡す。
	log.Println(srv.Run(context.Background()))
}
