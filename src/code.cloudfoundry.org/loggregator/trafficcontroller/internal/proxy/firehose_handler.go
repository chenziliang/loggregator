package proxy

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"code.cloudfoundry.org/loggregator/metricemitter"
	"code.cloudfoundry.org/loggregator/plumbing"

	"github.com/cloudfoundry/sonde-go/events"
	"github.com/gogo/protobuf/proto"
	"github.com/gorilla/mux"
)

type FirehoseHandler struct {
	server               *WebSocketServer
	grpcConn             grpcConnector
	counter              int64
	egressFirehoseMetric *metricemitter.Counter
}

func NewFirehoseHandler(grpcConn grpcConnector, w *WebSocketServer, m MetricClient) *FirehoseHandler {
	// metric-documentation-v2: (egress) Number of envelopes egressed via the
	// firehose.
	egressFirehoseMetric := m.NewCounter("egress",
		metricemitter.WithVersion(2, 0),
		metricemitter.WithTags(
			map[string]string{"endpoint": "firehose"},
		),
	)

	return &FirehoseHandler{
		grpcConn:             grpcConn,
		server:               w,
		egressFirehoseMetric: egressFirehoseMetric,
	}
}

func (h *FirehoseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&h.counter, 1)
	defer atomic.AddInt64(&h.counter, -1)

	subID := mux.Vars(r)["subID"]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var filter *plumbing.Filter
	switch r.URL.Query().Get("filter-type") {
	case "logs":
		filter = &plumbing.Filter{
			Message: &plumbing.Filter_Log{
				Log: &plumbing.LogFilter{},
			},
		}
	case "metrics":
		filter = &plumbing.Filter{
			Message: &plumbing.Filter_Metric{
				Metric: &plumbing.MetricFilter{},
			},
		}
	default:
		filter = nil
	}

	client, err := h.grpcConn.Subscribe(ctx, &plumbing.SubscriptionRequest{
		ShardID: subID,
		Filter:  filter,
	})
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		log.Printf("error occurred when subscribing to doppler: %s", err)
		return
	}

	// kchen for perf
	f := func() ([]byte, error) {
		envelope := getMessage(msgType)
		for {
			now := time.Now().UnixNano()
			envelope.Timestamp = &now
			envelope.LogMessage.Timestamp = &now

			data, err = proto.Marshal(envelope)
			if err != nil {
				continue
			}

			return data, nil
		}
	}
	_ = client
	h.server.serveWS(w, r, f, h.egressFirehoseMetric)
}

func (h *FirehoseHandler) Count() int64 {
	return atomic.LoadInt64(&h.counter)
}

func getMessage(msgType string) {
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
