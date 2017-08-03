package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"code.cloudfoundry.org/loggregator/metricemitter"
	"code.cloudfoundry.org/loggregator/plumbing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"

	"code.cloudfoundry.org/loggregator/trafficcontroller/app"
)

func main() {
	grpclog.SetLogger(log.New(ioutil.Discard, "", 0))
	disableAccessControl := flag.Bool(
		"disableAccessControl",
		false,
		"always all access to app logs",
	)
	configFile := flag.String(
		"config",
		"config/loggregator_trafficcontroller.json",
		"Location of the loggregator trafficcontroller config json file",
	)

	flag.Parse()

	conf, err := app.ParseConfig(*configFile)
	if err != nil {
		log.Panicf("Unable to parse config: %s", err)
	}

	credentials, err := plumbing.NewClientCredentials(
		conf.GRPC.CertFile,
		conf.GRPC.KeyFile,
		conf.GRPC.CAFile,
		"metron",
	)
	if err != nil {
		log.Fatalf("Could not use GRPC creds for client: %s", err)
	}

	// metric-documentation-v2: setup function
	metricClient, err := metricemitter.NewClient(
		conf.MetronConfig.GRPCAddress,
		metricemitter.WithGRPCDialOptions(grpc.WithTransportCredentials(credentials)),
		metricemitter.WithOrigin("loggregator.trafficcontroller"),
		metricemitter.WithPulseInterval(conf.MetricEmitterDuration),
	)
	if err != nil {
		log.Fatalf("Couldn't connect to metric emitter: %s", err)
	}

	go func() {
		err := oauth2Mockup(conf)
		if err != nil {
			log.Fatalf("Can't run oauth2Mockup: %s", err)
		}
	}()

	tc := app.NewTrafficController(
		conf,
		*disableAccessControl,
		metricClient,
		uaaHTTPClient(conf),
		ccHTTPClient(conf),
	)
	tc.Start()
}

func uaaHTTPClient(conf *app.Config) *http.Client {
	tlsConfig := plumbing.NewTLSConfig()
	tlsConfig.InsecureSkipVerify = conf.SkipCertVerify
	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     tlsConfig,
		DisableKeepAlives:   true,
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

func ccHTTPClient(conf *app.Config) *http.Client {
	tlsConfig, err := plumbing.NewClientMutualTLSConfig(
		conf.CCTLSClientConfig.CertFile,
		conf.CCTLSClientConfig.KeyFile,
		conf.CCTLSClientConfig.CAFile,
		conf.CCTLSClientConfig.ServerName,
	)
	if err != nil {
		log.Fatalf("Unable to create CC HTTP Client: %s", err)
	}
	tlsConfig.InsecureSkipVerify = conf.SkipCertVerify
	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     tlsConfig,
		DisableKeepAlives:   true,
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

func oauth2Mockup(conf *app.Config) error {
	// /v2/info
	info := []byte(`{"name":"","build":"","support":"https://support.pivotal.io","version":0,"description":"","authorization_endpoint":"","token_endpoint":"","min_cli_version":"6.23.0","min_recommended_cli_version":"6.23.0","api_version":"2.82.0","app_ssh_endpoint":"","app_ssh_host_key_fingerprint":"f3:f1:53:6d:dd:a3:94:37:0a:f8:ab:2b:3e:f7:56:27","app_ssh_oauth_client":"ssh-proxy","routing_endpoint":"","doppler_logging_endpoint":"","user":"5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb"}`)
	var v map[string]interface{}
	err := json.Unmarshal(info, &v)
	if err != nil {
		return err
	}

	v["authorization_endpoint"] = conf.AuthorizationEndpoint
	v["token_endpoint"] = conf.TokenEndpoint
	v["doppler_logging_endpoint"] = conf.DopplerLoggingEndpoint

	info, err = json.Marshal(v)
	if err != nil {
		return err
	}

	http.HandleFunc("/v2/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, string(info))
	})

	// Authorization code endpoint
	http.HandleFunc("/oauth/auth", func(w http.ResponseWriter, r *http.Request) {
	})

	// Access token endpoint
	http.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		// Should return acccess token back to the user
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		w.Write([]byte("access_token=mocktoken&scope=user&token_type=bearer"))
	})

	return http.ListenAndServe(":9911", nil)
}
