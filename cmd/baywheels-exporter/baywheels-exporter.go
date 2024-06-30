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

	"github.com/prometheus/client_golang/prometheus"
	"tailscale.com/tsnet"
	"tailscale.com/tsweb"
)

const BaywheelsURI = "https://gbfs.baywheels.com/gbfs/en"
const ListenPort = 8080

var runAsTsNet = flag.Bool("tsnet", false, "run as a tsnet service")

type StationInformation struct {
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

type StationInformationResponse struct {
	Data struct {
		Stations []StationInformation `json:"stations"`
	} `json:"data"`
}

type BikeStatus struct {
	BikeId     string  `json:"bike_id"`
	IsDisabled int     `json:"is_disabled"`
	IsReserved int     `json:"is_reserved"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
}

type BikeStatusResponse struct {
	Data struct {
		Bikes []BikeStatus `json:"bikes"`
	} `json:"data"`
}

type StationStatus struct {
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
		Stations []StationStatus `json:"stations"`
	} `json:"data"`
}

type BaywheelsMetrics struct {
	station_capacity         prometheus.GaugeVec
	bike_reserved            prometheus.GaugeVec
	bike_disabled            prometheus.GaugeVec
	station_last_report      prometheus.GaugeVec
	station_is_returning     prometheus.GaugeVec
	station_is_renting       prometheus.GaugeVec
	station_is_installed     prometheus.GaugeVec
	station_bikes_available  prometheus.GaugeVec
	station_bikes_disabled   prometheus.GaugeVec
	station_docks_available  prometheus.GaugeVec
	station_docks_disabled   prometheus.GaugeVec
	station_ebikes_available prometheus.GaugeVec
}

func (m *BaywheelsMetrics) Reset() {
	m.station_capacity.Reset()
	m.bike_reserved.Reset()
	m.bike_disabled.Reset()
	m.station_last_report.Reset()
	m.station_is_returning.Reset()
	m.station_is_renting.Reset()
	m.station_is_installed.Reset()
	m.station_bikes_available.Reset()
	m.station_bikes_disabled.Reset()
	m.station_docks_available.Reset()
	m.station_docks_disabled.Reset()
	m.station_ebikes_available.Reset()
}

func NewMetrics(reg prometheus.Registerer) *BaywheelsMetrics {
	m := &BaywheelsMetrics{
		station_capacity: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_capacity",
			Help: "Bike capacity of the station.",
		},
			[]string{"station_id", "name"},
		),

		bike_disabled: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bike_disabled",
			Help: "Bike is_disabled status",
		},
			[]string{"bike_id"},
		),
		bike_reserved: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bike_reserved",
			Help: "Bike is_reserved status",
		},
			[]string{"bike_id"},
		),
		station_last_report: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_last_report",
			Help: "Station status report last check-in timestamp",
		},
			[]string{"station_id"},
		),
		station_is_returning: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_is_returning",
			Help: "Station is_returning status",
		},
			[]string{"station_id"},
		),
		station_is_renting: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_is_renting",
			Help: "Station is_renting status",
		},
			[]string{"station_id"},
		),
		station_is_installed: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_is_installed",
			Help: "Station is_installed status",
		},
			[]string{"station_id"},
		),
		station_bikes_available: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_bikes_available",
			Help: "Number of bikes available at the station",
		},
			[]string{"station_id"},
		),
		station_bikes_disabled: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_bikes_disabled",
			Help: "Number of bikes disabled at the station",
		},
			[]string{"station_id"},
		),
		station_docks_available: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_docks_available",
			Help: "Number of docks available at the station",
		},
			[]string{"station_id"},
		),
		station_docks_disabled: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_docks_disabled",
			Help: "Number of docks disabled at the station",
		},
			[]string{"station_id"},
		),
		station_ebikes_available: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "station_ebikes_available",
			Help: "Number of ebikes available at the station",
		},
			[]string{"station_id"},
		),
	}
	reg.MustRegister(m.station_capacity)
	reg.MustRegister(m.bike_disabled)
	reg.MustRegister(m.bike_reserved)
	reg.MustRegister(m.station_last_report)
	reg.MustRegister(m.station_is_returning)
	reg.MustRegister(m.station_is_renting)
	reg.MustRegister(m.station_is_installed)
	reg.MustRegister(m.station_bikes_available)
	reg.MustRegister(m.station_bikes_disabled)
	reg.MustRegister(m.station_docks_available)
	reg.MustRegister(m.station_docks_disabled)
	reg.MustRegister(m.station_ebikes_available)

	return m
}

func sampleStationInformation(metrics *BaywheelsMetrics) {
	stationInformation, err := http.Get(fmt.Sprintf("%s/station_information.json", BaywheelsURI))
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

	var response StationInformationResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Printf("Error sampling station information %s\n", err)
		return
	} else {
		for _, station := range response.Data.Stations {
			metrics.station_capacity.With(prometheus.Labels{"station_id": station.StationId, "name": station.Name}).Set(float64(station.Capacity))
		}
	}
}

func sampleBikeInformation(metrics *BaywheelsMetrics) {
	bikeInformation, err := http.Get(fmt.Sprintf("%s/free_bike_status.json", BaywheelsURI))
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

	var response BikeStatusResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Printf("Error sampling bike status %s\n", err)
		return
	} else {
		for _, bike := range response.Data.Bikes {
			metrics.bike_disabled.With(prometheus.Labels{"bike_id": bike.BikeId}).Set(float64(bike.IsDisabled))
			metrics.bike_reserved.With(prometheus.Labels{"bike_id": bike.BikeId}).Set(float64(bike.IsReserved))
		}
	}
}

func sampleStationStatus(metrics *BaywheelsMetrics) {
	stationStatus, err := http.Get(fmt.Sprintf("%s/station_status.json", BaywheelsURI))
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
	} else {
		for _, station := range response.Data.Stations {
			// station stats
			metrics.station_last_report.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.LastReported))
			metrics.station_is_returning.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.IsReturning))
			metrics.station_is_renting.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.IsRenting))
			metrics.station_is_installed.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.IsInstalled))

			// pedal bike stats
			metrics.station_bikes_available.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.BikesAvailable))
			metrics.station_bikes_disabled.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.BikesDisabled))

			// dock stats
			metrics.station_docks_available.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.DocksAvailable))
			metrics.station_docks_disabled.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.DocksDisabled))

			// e-bike stats
			metrics.station_ebikes_available.With(prometheus.Labels{"station_id": station.StationId}).Set(float64(station.EBikesAvailable))
		}
	}

}

func sampleBaywheelsMetrics(metrics *BaywheelsMetrics) {
	metrics.Reset()
	sampleStationInformation(metrics)
	sampleStationStatus(metrics)
	sampleBikeInformation(metrics)
}

func main() {
	flag.Parse()
	metrics := NewMetrics(prometheus.DefaultRegisterer)
	ticker := time.NewTicker(60 * time.Second)

	// sample at startup
	sampleBaywheelsMetrics(metrics)

	// sample at 1 minute intervals
	go func() {
		for t := range ticker.C {
			log.Println("sampling at %s", t)
			sampleBaywheelsMetrics(metrics)
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
