FROM golang

ADD . /go/src/github.com/jchorl/munchbot
WORKDIR /go/src/github.com/jchorl/munchbot

RUN apt-get update
RUN apt-get -y install postgresql postgresql-contrib
RUN go get
RUN go install github.com/jchorl/munchbot

ENTRYPOINT make compose_run

EXPOSE 8080