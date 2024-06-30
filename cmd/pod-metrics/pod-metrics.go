package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gocolly/colly"
	"github.com/prometheus/client_golang/prometheus"
	"tailscale.com/tsnet"
	"tailscale.com/tsweb"
)

const ListenPort = 8080
const BaywheelsURL = "https://gbfs.baywheels.com/gbfs/en"
const COTLCushionURL = "https://merch.devolverdigital.com/products/cult-of-the-lamb-pillow"

type PODMetrics struct {
	// Cult of the Lamb pillow stock metrics
	cotl_pillow_last_check prometheus.Gauge
	cotl_pillow_in_stock   prometheus.Gauge

	// Baywheels bike metrics
	baywheels_bike_disabled prometheus.GaugeVec
	baywheels_bike_reserved prometheus.GaugeVec
	// Baywheels station metrics
	baywheels_station_bikes_available  prometheus.GaugeVec
	baywheels_station_bikes_disabled   prometheus.GaugeVec
	baywheels_station_capacity         prometheus.GaugeVec
	baywheels_station_docks_available  prometheus.GaugeVec
	baywheels_station_docks_disabled   prometheus.GaugeVec
	baywheels_station_ebikes_available prometheus.GaugeVec
	baywheels_station_is_installed     prometheus.GaugeVec
	baywheels_station_is_renting       prometheus.GaugeVec
	baywheels_station_is_returning     prometheus.GaugeVec
	baywheels_station_last_report      prometheus.GaugeVec
}

var runAsTsNet = flag.Bool("tsnet", false, "run as a tsnet service")

type BaywheelsStationInformation struct {
	Name                        string  `json:"name"`
	ShortName                   string  `json:"short_name"`
	StationId                   string  `json:"station_id"`
	StationType                 string  `json:"station_type"`
	Lat                         float64 `json:"lat"`
	Lon                         float64 `json:"lon"`
	ExternalId                  string  `json:"external_id"`
	Capacity                    int     `json:"capacity"`
	HasKiosk                    bool    `json:"has_kiosk"`
	ElectricBikeSurchargeWaiver bool    `json:"electric_bike_surcharge_waiver"`
}

type BaywheelsStationInformationResponse struct {
	Data struct {
		Stations []BaywheelsStationInformation `json:"stations"`
	} `json:"data"`
}

type BaywheelsBikeStatus struct {
	BikeId     string  `json:"bike_id"`
	IsDisabled int     `json:"is_disabled"`
	IsReserved int     `json:"is_reserved"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
}

type BaywheelsBikeStatusResponse struct {
	Data struct {
		Bikes []BaywheelsBikeStatus `json:"bikes"`
	} `json:"data"`
}

type BaywheelsStationStatus struct {
	StationId           string `json:"station_id"`
	IsInstalled         int    `json:"is_installed"`
	IsRenting           int    `json:"is_renting"`
	IsReturning         int    `json:"is_returning"`
	LastReported        int    `json:"last_reported"`
	BikesAvailable      int    `json:"num_bikes_available"`
	BikesDisabled       int    `json:"num_bikes_disabled"`
	DocksAvailable      int    `json:"num_docks_available"`
	DocksDisabled       int    `json:"num_docks_disabled"`
	EBikesAvailable     int    `json:"num_ebikes_available"`
	ScootersAvailable   int    `json:"num_scooters_available"`
	ScootersUnavailable int    `json:"num_scooters_unavailable"`
}

type StationStatusResponse struct {
	Data struct {
		Stations []BaywheelsStationStatus `json:"stations"`
	} `json:"data"`
}

func (m *PODMetrics) Reset() {
	m.baywheels_station_capacity.Reset()
	m.baywheels_bike_reserved.Reset()
	m.baywheels_bike_disabled.Reset()
	m.baywheels_station_last_report.Reset()
	m.baywheels_station_is_returning.Reset()
	m.baywheels_station_is_renting.Reset()
	m.baywheels_station_is_installed.Reset()
	m.baywheels_station_bikes_available.Reset()
	m.baywheels_station_bikes_disabled.Reset()
	m.baywheels_station_docks_available.Reset()
	m.baywheels_station_docks_disabled.Reset()
	m.baywheels_station_ebikes_available.Reset()
}

func NewMetrics(reg prometheus.Registerer) *PODMetrics {
	m := &PODMetrics{
		baywheels_station_capacity: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_capacity",
			Help: "Bike capacity of the station.",
		},
			[]string{"station_id", "name"},
		),

		baywheels_bike_disabled: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_bike_disabled",
			Help: "Bike is_disabled status",
		},
			[]string{"bike_id"},
		),
		baywheels_bike_reserved: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_bike_reserved",
			Help: "Bike is_reserved status",
		},
			[]string{"bike_id"},
		),
		baywheels_station_last_report: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_last_report",
			Help: "Station status report last check-in timestamp",
		},
			[]string{"station_id"},
		),
		baywheels_station_is_returning: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_is_returning",
			Help: "Station is_returning status",
		},
			[]string{"station_id"},
		),
		baywheels_station_is_renting: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_is_renting",
			Help: "Station is_renting status",
		},
			[]string{"station_id"},
		),
		baywheels_station_is_installed: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_is_installed",
			Help: "Station is_installed status",
		},
			[]string{"station_id"},
		),
		baywheels_station_bikes_available: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_bikes_available",
			Help: "Number of bikes available at the station",
		},
			[]string{"station_id"},
		),
		baywheels_station_bikes_disabled: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_bikes_disabled",
			Help: "Number of bikes disabled at the station",
		},
			[]string{"station_id"},
		),
		baywheels_station_docks_available: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_docks_available",
			Help: "Number of docks available at the station",
		},
			[]string{"station_id"},
		),
		baywheels_station_docks_disabled: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_docks_disabled",
			Help: "Number of docks disabled at the station",
		},
			[]string{"station_id"},
		),
		baywheels_station_ebikes_available: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "baywheels_station_ebikes_available",
			Help: "Number of ebikes available at the station",
		},
			[]string{"station_id"},
		),
		cotl_pillow_in_stock: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cotl_pillow_in_stock",
			Help: "Whether the Cult of the Lamb Pillow is in stock",
		}),
		cotl_pillow_last_check: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cotl_pillow_last_check",
			Help: "The last time the Cult of the Lamb Pillow was checked for stock",
		}),
	}
	reg.MustRegister(m.baywheels_station_capacity)
	reg.MustRegister(m.baywheels_bike_disabled)
	reg.MustRegister(m.baywheels_bike_reserved)
	reg.MustRegister(m.baywheels_station_last_report)
	reg.MustRegister(m.baywheels_station_is_returning)
	reg.MustRegister(m.baywheels_station_is_renting)
	reg.MustRegister(m.baywheels_station_is_installed)
	reg.MustRegister(m.baywheels_station_bikes_available)
	reg.MustRegister(m.baywheels_station_bikes_disabled)
	reg.MustRegister(m.baywheels_station_docks_available)
	reg.MustRegister(m.baywheels_station_docks_disabled)
	reg.MustRegister(m.baywheels_station_ebikes_available)

	reg.MustRegister(m.cotl_pillow_in_stock)
	reg.MustRegister(m.cotl_pillow_last_check)

	return m
}

func sampleStationInformation(metrics *PODMetrics) {
	stationInformation, err := http.Get(fmt.Sprintf("%s/station_information.json", BaywheelsURL))
	if err != nil {
		fmt.Printf("Error sampling station information %s\n", err)
		return
	}
	body, err := io.ReadAll(stationInformation.Body)
	defer stationInformation.Body.Close()
	if err != nil {
		fmt.Printf("Error sampling station information %s\n", err)
		return
	}

	var response BaywheelsStationInformationResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Printf("Error sampling station information %s\n", err)
		return
	} else {
		for _, station := range response.Data.Stations {
			metrics.baywheels_station_capacity.With(prometheus.Labels{"station_id": station.StationId, "name": station.Name}).Set(float64(station.Capacity))
		}
	}
}

func sampleBikeInformation(metrics *PODMetrics) {
	bikeInformation, err := http.Get(fmt.Sprintf("%s/free_bike_status.json", BaywheelsURL))
	if err != nil {
		fmt.Printf("Error sampling bike status %s\n", err)
		return
	}

	body, err := io.ReadAll(bikeInformation.Body)
	defer bikeInformation.Body.Close()
	if err != nil {
		fmt.Printf("Error sampling bike status %s\n", err)
		return
	}

	var response BaywheelsBikeStatusResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Printf("Error sampling bike status %s\n", err)
		return
	} else {
		for _, bike := range response.Data.Bikes {
			metrics.baywheels_bike_disabled.With(prometheus.Labels{"bike_id": bike.BikeId}).Set(float64(bike.IsDisabled))
			metrics.baywheels_bike_reserved.With(prometheus.Labels{"bike_id": bike.BikeId}).Set(float64(bike.IsReserved))
		}
	}
}

func sampleStationStatus(metrics *PODMetrics) {
	stationStatus, err := http.Get(fmt.Sprintf("%s/station_status.json", BaywheelsURL))
	if err != nil {
		fmt.Printf("Error sampling station status %s\n", err)
		return
	}

	body, err := io.ReadAll(stationStatus.Body)
	defer stationStatus.Body.Close()
	if err != nil {
		fmt.Printf("Error sampling station status %s\n", err)
		return
	}

	var response StationStatusResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Printf("Error sampling station status %s\n", err)
		return
	}

	for _, station := range response.Data.Stations {
		// station stats
		metrics.baywheels_station_last_report.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.LastReported))
		metrics.baywheels_station_is_returning.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.IsReturning))
		metrics.baywheels_station_is_renting.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.IsRenting))
		metrics.baywheels_station_is_installed.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.IsInstalled))

		// pedal bike stats
		metrics.baywheels_station_bikes_available.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.BikesAvailable))
		metrics.baywheels_station_bikes_disabled.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.BikesDisabled))

		// dock stats
		metrics.baywheels_station_docks_available.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.DocksAvailable))
		metrics.baywheels_station_docks_disabled.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.DocksDisabled))

		// e-bike stats
		metrics.baywheels_station_ebikes_available.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.EBikesAvailable))
	}
}

func sampleBaywheelsMetrics(metrics *PODMetrics) {
	metrics.Reset()
	sampleStationInformation(metrics)
	sampleStationStatus(metrics)
	sampleBikeInformation(metrics)
}

type cotlProbe struct {
	c       *colly.Collector
	metrics *PODMetrics
}

func newProbe(metrics *PODMetrics) cotlProbe {
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
	return cotlProbe{c: c, metrics: metrics}
}

func (p *cotlProbe) check() {
	log.Printf("Visiting %s", COTLCushionURL)
	if err := p.c.Visit(COTLCushionURL); err != nil {
		log.Printf("error scraping COTL pillow stock: %s", err)
	} else {
		p.metrics.cotl_pillow_last_check.SetToCurrentTime()
	}
}

func main() {
	flag.Parse()
	metrics := NewMetrics(prometheus.DefaultRegisterer)

	probe := newProbe(metrics)

	baywheelsTicker := time.NewTicker(60 * time.Second)
	cotlTicker := time.NewTicker(60 * time.Second * 5)

	// sample at startup
	probe.check()
	sampleBaywheelsMetrics(metrics)

	go func() {
		for {
			select {
			case <-cotlTicker.C:
				probe.check()
			case <-baywheelsTicker.C:
				sampleBaywheelsMetrics(metrics)
			}
		}
	}()

	var ln net.Listener
	var err error
	if *runAsTsNet {
		srv := tsnet.Server{
			Hostname: "baywheels-exporter",
			AuthKey:  os.Getenv("TS_AUTHKEY"),
			Logf:     log.Printf,
		}
		ln, err = srv.Listen("tcp", ":80")
		if err != nil {
			log.Fatal(err)
		}
	} else {
		ln, err = net.Listen("tcp", fmt.Sprintf(":%d", ListenPort))
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("listening on %s", ln.Addr().String())
	}

	mux := http.NewServeMux()
	tsweb.Debugger(mux)
	log.Fatal(http.Serve(ln, mux))
}
