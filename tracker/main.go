package main

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"tracker/ks"
	"tracker/trip"

	"github.com/google/uuid"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// where Tracker is located to
var whereami string

// Get the URL for next call from env variable
func getNextCall() string {
	return os.Getenv("NEXT_CALL")
}

// API (/trip) - get TripDetail
func startTrip(c *gin.Context) {
	// Initial TripDetail with UUID
	tp := &trip.TripDetail{
		Id: uuid.New().String(),
	}

	// Build source pod from headers if call from pod
	whoami := c.Param("whoami")
	srcPod := &ks.Pod{}
	if whoami == "pod" {
		srcPod.Name = c.Request.Header.Get("x-pod-name")
		srcPod.Namespace = c.Request.Header.Get("x-pod-namespace")
		srcPod.NodeName = c.Request.Header.Get("x-node-name")
		srcPod.NodeIp = c.Request.Header.Get("x-node-ip")
		srcPod.Zone = c.Request.Header.Get("x-zone")
		srcPod.PodIp = c.Request.Header.Get("x-pod-ip")

	}
	if err := tp.CallTrip(srcPod, getNextCall()); err != nil {
		log.Error().Interface("err", err).Msg("tp.CallTrip")
	}
	log.Info().Int("p2p_num", len(tp.Detail)).Send()

	//Get one-way trip latency: A->B / put time into header & calculate
	start, _ := strconv.ParseInt(c.Request.Header.Get("x-request-start"), 10, 64)
	end := time.Now().UnixNano() / int64(time.Millisecond)
	// One time call and latency is 0
	if c.Request.Header.Get("x-request-start") == "" {
		start = end
	}
	// Get remote client IP if it's first call
	cltIp := c.Request.Header.Get("x-pod-ip")
	if cltIp == "" {
		//TODO: Get external IP when call from inside Pod/? Call API?
		cltIp = c.ClientIP()
	}

	//Source
	src := trip.Point{
		Ip: cltIp,
	}
	if whoami == "pod" {
		src.Pod = srcPod
	}

	wp := trip.Point{
		Ip: whereami,
	}
	var swp trip.Point
	if c.Request.Header.Get("x-pod-ip") == "" {
		fp2p := trip.P2p{
			Number:      0,
			Source:      src,
			Destination: wp,
		}
		tp.Detail = append(tp.Detail, fp2p)
		swp = wp
	} else {
		swp = src
	}

	//Destination
	dstPod := &ks.Pod{}
	p2p := trip.P2p{
		Number: len(tp.Detail),
		Source: swp,
		Destination: trip.Point{
			Ip:  dstPod.GetLocalIP(),
			Pod: dstPod.BuildPod(),
		},
		Method:     c.Request.Method,
		RequestURI: c.Request.RequestURI,
		Response:   dstPod.BuildResponse(),
		Latency:    end - start,
	}
	tp.Detail = append(tp.Detail, p2p)
	log.Info().Interface("return", tp.Detail).Msg("startTrip()")
	// Save to redis, only once
	if whoami != "pod" {
		buf, _ := json.Marshal(tp.Detail)
		if err := trip.SaveTd2Redis(tp.Id, buf); err != nil {
			log.Error().Interface("error", err).Msg("fail to trip.SaveTd2Redis()")
		}
	}
	c.JSON(http.StatusOK, tp.Detail)
}

// API (/initial) - get all pods as per request
func getInitialPods(c *gin.Context) {
	from := c.Param("from")
	whereami = from
	buf, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		log.Error().Interface("err", err).Msg("Read request body")
	}
	log.Info().Str("request_str", string(buf)).Send()

	strs := strings.Split(string(buf), "::")
	ns := strs[0]
	prefixs := strings.Split(strs[1], ",")
	log.Info().Strs("prefixs", prefixs).Send()

	// Initial TripDetail with UUID
	tp := &trip.TripDetail{
		Id: uuid.New().String(),
	}

	if err = tp.GetInitialPods(whereami, ns, prefixs); err != nil {
		log.Error().Interface("error", err).Msg("getInitialPods()")
		c.JSON(http.StatusInternalServerError, err)
	} else {
		log.Info().Interface("return", tp.Detail).Msg("getInitialPods()")
		c.JSON(http.StatusOK, tp.Detail)
	}

}

// Test panic and reboot Pod
func doPanic(c *gin.Context) {
	log.Info().Str("exit_code", "255").Msg("Received panic request for demo")
	os.Exit(255)
}

// Load all trips from storage
func allTrips(c *gin.Context) {
	// Initial TripDetail with UUID
	tp := &trip.TripDetail{
		Id: uuid.New().String(),
	}
	if err := tp.TripHistory(); err != nil {
		log.Error().Interface("error", err).Msg("allTrips()")
		c.JSON(http.StatusInternalServerError, err)
	} else {
		log.Info().Interface("return", tp.Detail).Msg("allTrips()")
		c.JSON(http.StatusOK, tp.Detail)
	}

}

// Clear trip history in redis
func clearTrips(c *gin.Context) {
	trip.ClearHistory()
	c.String(http.StatusOK, "Cleared")
}

func router(ctx context.Context, r *gin.Engine) *gin.Engine {
	log.Info().Interface("ctx", ctx).Msg("context.Context pairs")
	r.GET("/trip", startTrip)
	r.GET("/trip/:whoami", startTrip)
	r.POST("/initial/:from", getInitialPods)

	// ko add static assets under ./kodata - https://github.com/google/ko#static-assets
	if staticDir := os.Getenv("KO_DATA_PATH"); staticDir != "" {
		log.Info().Str("KO_DATA_PATH", staticDir).Send()
		r.Static("tracker-ui", staticDir)
	} else {
		r.Static("tracker-ui", "./kodata")
	}

	r.GET("/panic", doPanic)
	r.GET("/all-trips", allTrips)
	r.GET("/clear-trips", clearTrips)
	return r
}

func main() {
	whereami = "gcp"
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if gin.IsDebugging() {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	gin.DisableConsoleColor()
	server := gin.Default()

	//setup CORS policies
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = true
	server.Use(cors.Default())

	if port := os.Getenv("POD_PORT"); port != "" {
		log.Fatal().Err(router(context.Background(), server).Run("0.0.0.0:" + port))
	} else {
		log.Fatal().Err(router(context.Background(), server).Run("0.0.0.0:8000"))
	}

}
