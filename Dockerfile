FROM golang

ADD . /go/src/github.com/jchorl/munchbot
RUN go install github.com/jchorl/munchbot

ENTRYPOINT /go/bin/munchbot

EXPOSE 8080