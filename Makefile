clean:
	rm -rf kvraft-*

put:
	curl -X PUT localhost:12380 -d '{"key": "hello", "value": "raft"}'

get:
	curl -X GET localhost:12380/hello

post:
	curl -X POST localhost:12380 -d '{"id": "4", "url": "http://127.0.0.1:42379"}'

delete:
	curl -X DELETE localhost:12380 -d '{"id": "4", "url": "http://127.0.0.1:42379"}'
