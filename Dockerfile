FROM golang

ADD . /go/src/github.com/jchorl/munchbot
WORKDIR /go/src/github.com/jchorl/munchbot

RUN go get
RUN go install github.com/jchorl/munchbot

ENTRYPOINT /go/bin/munchbot

EXPOSE 8080