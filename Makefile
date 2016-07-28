build:
	docker build -t jchorl/munchbot .

run:
	docker run --rm -it -p 8080:8080 jchorl/munchbot