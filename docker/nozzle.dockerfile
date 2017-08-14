FROM golang:1.8.3

RUN apt-get update && apt-get install -y telnet
ENV GOPATH /go
RUN go get -d github.com/cloudfoundry-community/splunk-firehose-nozzle; exit 0
WORKDIR /go/src/github.com/cloudfoundry-community/splunk-firehose-nozzle
RUN git checkout develop
RUN cd /go/src/github.com/cloudfoundry-community/splunk-firehose-nozzle && make build
WORKDIR /go/src/github.com/cloudfoundry-community/splunk-firehose-nozzle

ENV HEC_WORKERS=8
ENV SKIP_SSL_VALIDATION=true
ENV ADD_APP_INFO=true

ENV API_ENDPOINT=http://trafficcontroller:9911
ENV API_USER=admin
ENV API_PASSWORD=admin

ENV EVENTS=ValueMetric,CounterEvent,Error,LogMessage,HttpStartStop,ContainerMetric
ENV SPLUNK_TOKEN=1CB57F19-DC23-419A-8EDA-BA545DD3674D
ENV SPLUNK_HOST=https://heclb1:8088
ENV SPLUNK_INDEX=main
ENV FLUSH_INTERVAL=30s
ENV FIREHOSE_SUBSCRIPTION_ID=kchen-spl

CMD ["/bin/sh", "-c", "sleep 60 && ./splunk-firehose-nozzle"]
