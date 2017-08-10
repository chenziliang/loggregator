FROM ubuntu:16.04

ADD etcd-v3.2.2-linux-amd64 /etcd-v3.2.2-linux-amd64
RUN apt-get update && apt-get install -y telnet

EXPOSE 2379
WORKDIR /etcd-v3.2.2-linux-amd64

CMD ["./etcd", "--listen-client-urls", "http://0.0.0.0:2379", "--advertise-client-urls", "http://etcd:2379"]
