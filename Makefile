build:
	docker build -t jchorl/munchbot .

run:
	docker run --rm -it -p 8080:8080 jchorl/munchbot

pg_start:
	service postgresql start

drop_db:
	echo "munch" | dropdb -h $(DB_PORT_5432_TCP_ADDR) -U munch usertokens

create_db:
	echo "munch" | createdb -h $(DB_PORT_5432_TCP_ADDR) -U munch usertokens

wait:
	bash wait

run_binary:
	/go/bin/munchbot
	
compose_run: wait pg_start drop_db create_db run_binary