package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"code.cloudfoundry.org/loggregator/healthendpoint"

	"code.cloudfoundry.org/loggregator/dopplerservice"
	"code.cloudfoundry.org/loggregator/metricemitter"
	"code.cloudfoundry.org/loggregator/monitor"
	"code.cloudfoundry.org/loggregator/plumbing"
	"code.cloudfoundry.org/loggregator/profiler"
	"code.cloudfoundry.org/loggregator/trafficcontroller/internal/auth"
	"code.cloudfoundry.org/loggregator/trafficcontroller/internal/proxy"

	"code.cloudfoundry.org/workpool"
	"github.com/cloudfoundry/dropsonde"
	"github.com/cloudfoundry/dropsonde/emitter"
	"github.com/cloudfoundry/dropsonde/envelope_sender"
	"github.com/cloudfoundry/dropsonde/envelopes"
	"github.com/cloudfoundry/dropsonde/log_sender"
	"github.com/cloudfoundry/dropsonde/logs"
	"github.com/cloudfoundry/dropsonde/metric_sender"
	"github.com/cloudfoundry/dropsonde/metricbatcher"
	"github.com/cloudfoundry/dropsonde/metrics"
	"github.com/cloudfoundry/dropsonde/runtime_stats"
	"github.com/cloudfoundry/sonde-go/events"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/gogo/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// MetricClient creates new CounterMetrics to be emitted periodically.
type MetricClient interface {
	NewCounter(name string, opts ...metricemitter.MetricOption) *metricemitter.Counter
	NewGauge(name, unit string, opts ...metricemitter.MetricOption) *metricemitter.Gauge
}

type TrafficController struct {
	conf                 *Config
	disableAccessControl bool
	metricClient         MetricClient
	uaaHTTPClient        *http.Client
	ccHTTPClient         *http.Client
	killChan             chan os.Signal
}

// finder provides service discovery of Doppler processes
type finder interface {
	Start()
	Next() dopplerservice.Event
}

func NewTrafficController(
	c *Config,
	disableAccessControl bool,
	metricClient MetricClient,
	uaaHTTPClient *http.Client,
	ccHTTPClient *http.Client,
) *TrafficController {
	return &TrafficController{
		conf:                 c,
		disableAccessControl: disableAccessControl,
		metricClient:         metricClient,
		uaaHTTPClient:        uaaHTTPClient,
		ccHTTPClient:         ccHTTPClient,
		killChan:             make(chan os.Signal),
	}
}

func (t *TrafficController) Start() {
	data, _ := json.Marshal(t.conf)
	log.Printf("Startup: Setting up the loggregator traffic controller, config:\n%s\n", data)

	batcher, err := t.initializeMetrics("LoggregatorTrafficController", t.conf.MetronConfig.UDPAddress)
	if err != nil {
		log.Printf("Error initializing dropsonde: %s", err)
	}
	_ = batcher

	monitorInterval := time.Duration(t.conf.MonitorIntervalSeconds) * time.Second
	uptimeMonitor := monitor.NewUptime(monitorInterval)
	go uptimeMonitor.Start()
	defer uptimeMonitor.Stop()

	openFileMonitor := monitor.NewLinuxFD(monitorInterval)
	go openFileMonitor.Start()
	defer openFileMonitor.Stop()

	logAuthorizer := auth.NewLogAccessAuthorizer(
		t.ccHTTPClient,
		t.disableAccessControl,
		t.conf.ApiHost,
	)

	uaaClient := auth.NewUaaClient(
		t.uaaHTTPClient,
		t.conf.UaaHost,
		t.conf.UaaClient,
		t.conf.UaaClientSecret,
	)
	adminAuthorizer := auth.NewAdminAccessAuthorizer(t.disableAccessControl, &uaaClient)

	// Start the health endpoint listener
	promRegistry := prometheus.NewRegistry()
	healthendpoint.StartServer(t.conf.HealthAddr, promRegistry)
	healthRegistry := healthendpoint.New(promRegistry, map[string]prometheus.Gauge{
		// metric-documentation-health: (firehoseStreamCount)
		// Number of open firehose streams
		"firehoseStreamCount": prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "loggregator",
				Subsystem: "trafficcontroller",
				Name:      "firehoseStreamCount",
				Help:      "Number of open firehose streams",
			},
		),
		// metric-documentation-health: (appStreamCount)
		// Number of open app streams
		"appStreamCount": prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "loggregator",
				Subsystem: "trafficcontroller",
				Name:      "appStreamCount",
				Help:      "Number of open app streams",
			},
		),
	})

	creds, err := plumbing.NewClientCredentials(
		t.conf.GRPC.CertFile,
		t.conf.GRPC.KeyFile,
		t.conf.GRPC.CAFile,
		"doppler",
	)
	if err != nil {
		log.Fatalf("Could not use GRPC creds for server: %s", err)
	}

	var f finder
	switch {
	case len(t.conf.DopplerAddrs) > 0:
		f = plumbing.NewStaticFinder(t.conf.DopplerAddrs)
	default:
		etcdAdapter := t.defaultStoreAdapterProvider(t.conf)
		for {
			err = etcdAdapter.Connect()
			if err != nil {
				log.Printf("Unable to connect to ETCD: %s", err)
				time.Sleep(time.Second)
			} else {
				break
			}
		}

		f = dopplerservice.NewFinder(
			etcdAdapter,
			int(t.conf.DopplerPort),
			int(t.conf.GRPC.Port),
			[]string{"ws"},
			"",
		)
	}

	f.Start()
	pool := plumbing.NewPool(20, grpc.WithTransportCredentials(creds))
	_ = pool
	// grpcConnector := plumbing.NewGRPCConnector(1000, pool, f, batcher, t.metricClient)
	grpcConnector := &grpcConnectorPerf{
		msgType:           t.conf.MessageTypeToSimulate,
		trafficController: t,
	}

	dopplerHandler := http.Handler(
		proxy.NewDopplerProxy(
			logAuthorizer,
			adminAuthorizer,
			grpcConnector,
			"doppler."+t.conf.SystemDomain,
			5*time.Second,
			t.metricClient,
			healthRegistry,
		),
	)

	var accessMiddleware func(http.Handler) *auth.AccessHandler
	if t.conf.SecurityEventLog != "" {
		accessLog, err := os.OpenFile(t.conf.SecurityEventLog, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
		if err != nil {
			log.Panicf("Unable to open access log: %s", err)
		}
		defer func() {
			accessLog.Sync()
			accessLog.Close()
		}()
		accessLogger := auth.NewAccessLogger(accessLog)
		accessMiddleware = auth.Access(accessLogger, t.conf.IP, t.conf.OutgoingDropsondePort)
	}

	if accessMiddleware != nil {
		dopplerHandler = accessMiddleware(dopplerHandler)
	}
	go func() {
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", t.conf.OutgoingDropsondePort), dopplerHandler))
	}()

	// We start the profiler last so that we can definitively claim that we're ready for
	// connections by the time we're listening on the PPROFPort.
	p := profiler.New(t.conf.PPROFPort)
	go p.Start()

	signal.Notify(t.killChan, os.Interrupt)

	<-t.killChan
	log.Print("Shutting down")
}

func (t *TrafficController) setupDefaultEmitter(origin, destination string) error {
	if origin == "" {
		return errors.New("Cannot initialize metrics with an empty origin")
	}

	if destination == "" {
		return errors.New("Cannot initialize metrics with an empty destination")
	}

	udpEmitter, err := emitter.NewUdpEmitter(destination)
	if err != nil {
		return fmt.Errorf("Failed to initialize dropsonde: %v", err.Error())
	}

	dropsonde.DefaultEmitter = emitter.NewEventEmitter(udpEmitter, origin)
	return nil
}

func (t *TrafficController) initializeMetrics(origin, destination string) (*metricbatcher.MetricBatcher, error) {
	err := t.setupDefaultEmitter(origin, destination)
	if err != nil {
		// Legacy holdover.  We would prefer to panic, rather than just throwing our metrics
		// away and pretending we're running fine, but for now, we just don't want to break
		// anything.
		dropsonde.DefaultEmitter = &dropsonde.NullEventEmitter{}
	}

	// Copied from dropsonde.initialize(), since we stopped using
	// dropsonde.Initialize but needed it to continue operating the same.
	sender := metric_sender.NewMetricSender(dropsonde.DefaultEmitter)
	batcher := metricbatcher.New(sender, time.Second)
	metrics.Initialize(sender, batcher)
	logs.Initialize(log_sender.NewLogSender(dropsonde.DefaultEmitter))
	envelopes.Initialize(envelope_sender.NewEnvelopeSender(dropsonde.DefaultEmitter))
	go runtime_stats.NewRuntimeStats(dropsonde.DefaultEmitter, 10*time.Second).Run(nil)
	http.DefaultTransport = dropsonde.InstrumentedRoundTripper(http.DefaultTransport)
	return batcher, err
}

func (t *TrafficController) defaultStoreAdapterProvider(conf *Config) storeadapter.StoreAdapter {
	workPool, err := workpool.NewWorkPool(conf.EtcdMaxConcurrentRequests)
	if err != nil {
		log.Panic(err)
	}
	options := &etcdstoreadapter.ETCDOptions{
		ClusterUrls: conf.EtcdUrls,
	}
	if conf.EtcdRequireTLS {
		options.IsSSL = true
		options.CertFile = conf.EtcdTLSClientConfig.CertFile
		options.KeyFile = conf.EtcdTLSClientConfig.KeyFile
		options.CAFile = conf.EtcdTLSClientConfig.CAFile
	}
	etcdStoreAdapter, err := etcdstoreadapter.New(options, workPool)
	if err != nil {
		log.Panic(err)
	}
	return etcdStoreAdapter
}

func (t *TrafficController) Shutdown() {
	t.killChan <- os.Interrupt
}

// kchen for perf
type grpcConnectorPerf struct {
	msgType           string
	trafficController *TrafficController
}

func (g *grpcConnectorPerf) Subscribe(ctx context.Context, req *plumbing.SubscriptionRequest) (func() ([]byte, error), error) {
	start := time.Now().UnixNano()
	f := func() ([]byte, error) {
		envelope := getMessage(g.msgType)
		for {
			now := time.Now().UnixNano()
			if now-start > int64(g.trafficController.conf.RunDuration) {
				g.trafficController.Shutdown()
			}

			envelope.Timestamp = &now
			envelope.LogMessage.Timestamp = &now

			data, err := proto.Marshal(envelope)
			if err != nil {
				continue
			}

			return data, nil
		}
	}
	return f, nil
}

func (g *grpcConnectorPerf) ContainerMetrics(ctx context.Context, appID string) [][]byte {
	return nil
}

func (g *grpcConnectorPerf) RecentLogs(ctx context.Context, appID string) [][]byte {
	return nil
}

func getMessage(msgType string) *events.Envelope {
	var msg []byte
	if msgType == "s1kbyte" {
		msg = []byte(`{"@timestamp":"2017-07-18T22:48:59.763Z","@version":1,"annotation":"PR-34 uuid=1501606313280793319 generate data id=21367","class":"com.proximetry.dsc2.listners.Dsc2SubsystemAmqpListner","file":"Dsc2SubsystemAmqpListner.java","level":"INFO","line_number":"101","logger_name":"com.proximetry.dsc2.listners.Dsc2SubsystemAmqpListner","mdc":{"bundle.id":97,"bundle.    name":"com.proximetry.dsc2","bundle.version":"0.0.1.SNAPSHOT"},"message":"\"127.0.0.1 - admin [29/Apr/2017:17:53:05.154 -0700] \"GET /zh-CN/ HTTP/1.1\" 303 105 \"http://localhost:8000/zh-CN/account/login?return_to=%2Fzh-CN%2F\" \"Mozilla/5.0 (Macintosh; Intel Mac OS X10_12_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/57.0.2987.133 Safari/537.36\" - 5905357127106c6ce50 23ms\"","method":"spawnNewSubsystemHandler","source_host":"1ajkpfgpagq","thread_name":"bundle-97-ActorSystem-akka.actor.default-dispatcher-5","log":"127.0.0.1 - admin [29/Apr/2017:17:53:05.154 -0700] GET /zh-CN/ HTTP/1.1 303 105 http://localhost:8000/zh-CN/account/login?return_to=%2Fzh-CN%2F Mozilla/5.0 (Macintosh; Intel Mac OS X10_12_4) AppleWebKit/537.36 (KHTML, like Gecko)"}`)
	} else if msgType == "s256byte" {
		msg = []byte(`{"@timestamp":"2017-07-18T22:48:59.763Z","class":"com.proximetry.dsc2.listners.Dsc2SubsystemAmqpListner","file":"Dsc2SubsystemAmqpListner.java","level":"INFO","line_number":"101","method":"spawnNewSubsystemHandler","source_host":"1ajkpfgpagq"}`)
	} else if msgType == "uns1kbyte" {
		msg = []byte(`127.0.0.1 - - [29/Apr/2017:17:52:57.962 -0700] "GET /zh-CN/static/@1FFB5B3691CDDD837FB53E2D652D5DD69058B047CE286DD4DB8D00D952746A3E/js/i18n.js HTTP/1.1" 200 61117 "http://localhost:8000/zh-CN/account/    login?return_to=%2Fzh-CN%2F" "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/57.0.2987.133 Safari/537.36" - 59053569f6106a35090 96ms 127.0.0.1 - - [29/Apr/2017:17:52:58.624 -0700] "GET /zh-CN/static/@1FFB5B3691CDDD837FB53E2D652D5DD69058B047CE286DD4DB8D00D952746A3E/fonts/roboto-regular-webfont.woff HTTP/1.1" 200 114536 "http://localhost:8000/zh-CN/static/@1FFB5B3691CDDD837FB53E2D652D5DD69058B047CE286DD4DB8D00D952746A3E/build/css/bootstrap-enterprise.css" "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/57.0.2987.133 Safari/537.36" - 5905356a9f106a350d0 8ms 127.0.0.1 - - [29/Apr/2017:17:52:58.625 -0700] "GET /zh-CN/static/@1FFB5B3691CDDD837FB53E2D652D5DD69058B047CE286DD4DB8D00D952746A3E/fonts/splunkicons-regular-webfont.woff HTTP/1.1" 200 13792 "http://localhost:8000/zh-CN/static/@1FFB5B3691CDDD837FB53E2D652D5DD69058B047CE286DD4DB8D00D952746A3E/build/css/bootstrap-enterprise.css"`)
	} else if msgType == "uns256byte" {
		msg = []byte(`127.0.0.1 - - [29/Apr/2017:17:52:57.962 -0700] "GET /zh-CN/static/@1FFB5B3691CDDD837FB53E2D652D5DD69058B047CE286DD4DB8D00D952746A3E/js/i18n.js HTTP/1.1" 200 61117 "http://localhost:8000/zh-CN/account/login?return_to=%2Fzh-CN%2F" "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_4)"`)
	}

	data := []byte(`{"origin":"firehose","eventType":"LogMessage","timestamp":0,"deployment":"cf","job":"diego_cell","index":"d3b24497-2ff9-41d0-a687-db8a34fc810d","ip":"192.168.16.24","tags":{"firehose":"data-gen-simulator"},"logMessage":{"message":"","message_type":1,"timestamp":0,"app_id":"913339ce-e25f-4193-8665-bfaf2d77970a","source_type":"APP/PROC/WEB","source_instance":"0"}}`)
	envelope := &events.Envelope{}
	err := json.Unmarshal(data, envelope)
	if err != nil {
		panic(err)
	}

	envelope.LogMessage.Message = msg

	return envelope
}
