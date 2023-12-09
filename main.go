package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"tailscale.com/tsnet"
)

var (
	target   = flag.String("target", "", "the HTTP upstream target to proxy")
	hostname = flag.String("hostname", "", "the hostname for your service on the tsnet")
)

func main() {
	flag.Parse()

	if *target == "" {
		log.Fatal("target is required")
	}
	if *hostname == "" {
		log.Fatal("hostname is required")
	}

	upstream, err := url.Parse(*target)
	if err != nil {
		log.Fatal("target is not a valid URL: ", err)
	}

	srv := tsnet.Server{
		Hostname: *hostname,
		AuthKey:  os.Getenv("TS_AUTHKEY"),
		Logf:     log.Printf,
	}

	tls, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}

	p := httputil.NewSingleHostReverseProxy(upstream)

	log.Fatal(http.Serve(tls, p))

}
