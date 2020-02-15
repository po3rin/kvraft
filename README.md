# kvraft

技術書典8でgolang.tokyoが流布した「Gopherの休日 2020冬」の第n章「GoとコンセンサスアルゴリズムRaftによる分散システム構築入門」のサンプルコードです。

## Quick start

```bash
$ go get github.com/mattn/goreman
$ goreman start

$ curl -X PUT localhost:12380 -d '{"key": "hello", "value": "raft"}'
$ curl -X GET localhost:22380/hello
{"hello":"raft"}
```
