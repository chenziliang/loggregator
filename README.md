# standalone Loggregator for perf / function testing ## How to build and deploy * go get github.com/chenziliang/loggregator
* export GOPATH=$GOPATH/src/github.com/chenziliang/loggregator  # override GOPATH
* cd $GOPATH && ./scripts/build
* update loggregator_trafficcontroller.json to refect your env, specifically the key/crt path and hostname (replace `ghost` to your public hostname)
* ./bin/trafficcontroller --config loggregator_trafficcontroller.json --disableAccessControl

## Setup splunk nozzle to subscribe events from this standalone Loggregator
* export API_ENDPOINT=http://\<loggregator hostname\>:9911
* export API_USER=admin
* export API_PASSWORD=admin
* export SKIP_SSL_VALIDATION=true
* export ADD_APP_INFO=true
* export EVENTS=LogMessage
* export SPLUNK_TOKEN=\<HEC token\>
* export SPLUNK_HOST=\<HEC endpoint\>
* export SPLUNK_HOST=\<HEC endpoint\>
* export SPLUNK_INDEX=main
* export FLUSH_INTERVAL=30s
* export FIREHOSE_SUBSCRIPTION_ID=\<your subscription ID\>
* ./splunk-firehose-nozzle
