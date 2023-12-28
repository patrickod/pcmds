package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gocolly/colly/v2"
	"tailscale.com/tsnet"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const COTL_CUSHION_URL = "https://merch.devolverdigital.com/products/cult-of-the-lamb-pillow"

type CultOfTheLambPillowMetrics struct {
	cotl_pillow_last_check prometheus.Gauge
	cotl_pillow_in_stock   prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *CultOfTheLambPillowMetrics {
	m := &CultOfTheLambPillowMetrics{
		cotl_pillow_in_stock: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cotl_pillow_in_stock",
			Help: "Whether the Cult of the Lamb Pillow is in stock",
		}),
		cotl_pillow_last_check: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cotl_pillow_last_check",
			Help: "The last time the Cult of the Lamb Pillow was checked for stock",
		}),
	}
	reg.MustRegister(m.cotl_pillow_in_stock)
	reg.MustRegister(m.cotl_pillow_last_check)

	return m
}

func main() {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	ticker := time.NewTicker(60 * time.Second)

	c := colly.NewCollector()
	c.OnHTML("#product-form .product-submit", func(e *colly.HTMLElement) {
		disabled := e.ChildAttr("input", "disabled")
		// non-empty disabled attribute on submit indicates out of stock
		if len(disabled) > 0 {
			log.Printf("Cult of the Lamb Pillow out of stock")
			metrics.cotl_pillow_in_stock.Set(0)
		} else {
			metrics.cotl_pillow_in_stock.Set(1)
			log.Printf("Cult of the Lamb Pillow IS IN STOCK")
		}
	})

	// check action
	check := func() {
		log.Printf("Visiting %s", COTL_CUSHION_URL)
		c.Visit(COTL_CUSHION_URL)
		metrics.cotl_pillow_last_check.SetToCurrentTime()
	}
	// set initial state
	check()

	// refresh on the ticker
	go func() {
		for range ticker.C {
			check()
		}
	}()

	srv := tsnet.Server{
		Hostname: "cotl-probe",
		AuthKey:  os.Getenv("TS_AUTHKEY"),
		Logf:     log.Printf,
	}
	http80, err := srv.Listen("tcp", ":80")
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{Registry: registry}))
	log.Fatal(http.Serve(http80, nil))
}
