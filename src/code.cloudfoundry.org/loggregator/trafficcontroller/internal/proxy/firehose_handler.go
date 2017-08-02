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
		data := []byte(`{"origin":"firehose","eventType":"LogMessage","timestamp":0,"deployment":"cf","job":"diego_cell","index":"d3b24497-2ff9-41d0-a687-db8a34fc810d","ip":"192.168.16.24","tags":{"firehose":"data-gen-simulator"},"logMessage":{"message":"","message_type":1,"timestamp":0,"app_id":"566dfcdb-2c68-4a16-b9a0-bf0bc5518e02","source_type":"APP/PROC/WEB","source_instance":"0"}}`)
		envelope := &events.Envelope{}
		err := json.Unmarshal(data, envelope)
		if err != nil {
			panic(err)
		}

		msg := []byte(`{"@timestamp":"2017-07-18T22:48:59.763Z","@version":1,"annotation":"PR-34 uuid=1501606313280793319 generate data id=21367","class":"com.proximetry.dsc2.listners.Dsc2SubsystemAmqpListner","file":"Dsc2SubsystemAmqpListner.java","level":"INFO","line_number":"101","logger_name":"com.proximetry.dsc2.listners.Dsc2SubsystemAmqpListner","mdc":{"bundle.id":97,"bundle.name":"com.proximetry.dsc2","bundle.version":"0.0.1.SNAPSHOT"},"message":"blahblah-blah|blahblahblah|dsc2| KeyIdRequest :KeyIdRequest(key:xxxxxxxxxxx, id:-xxxxxxxxxxxxxxxxxxx)","method":"spawnNewSubsystemHandler","source_host":"1ajkpfgpagq","thread_name":"bundle-97-ActorSystem-akka.actor.default-dispatcher-5"}`)

		now := time.Now().Unix()
		envelope.Timestamp = &now
		envelope.LogMessage.Message = msg
		envelope.LogMessage.Timestamp = &now

		data, err = proto.Marshal(envelope)
		if err != nil {
			panic(err)
		}

		for {
			return data, nil
		}
	}
	_ = client
	h.server.serveWS(w, r, f, h.egressFirehoseMetric)
}

func (h *FirehoseHandler) Count() int64 {
	return atomic.LoadInt64(&h.counter)
}
