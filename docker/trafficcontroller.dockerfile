FROM golang:1.8.3

RUN apt-get update && apt-get install -y telnet
ENV GOPATH /go
RUN go get -d github.com/chenziliang/loggregator; exit 0
WORKDIR /go/src/github.com/chenziliang/loggregator
RUN git checkout feature/firehose-standalone
ENV GOPATH /go/src/github.com/chenziliang/loggregator
RUN cd /go/src/github.com/chenziliang/loggregator && ./scripts/build
WORKDIR /go/src/github.com/chenziliang/loggregator
EXPOSE 9911
CMD ["./bin/trafficcontroller", "--config", "loggregator_trafficcontroller.json", "--disableAccessControl"]
