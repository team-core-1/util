# `util`
Go utility

## Go
* version: v1.25.5

## Type
* openapi
* mempool
* queue

## Test
1. 파일과 테스트 함수 지정
```bash
$ go mod init mempool
$ go test -v -race mempool.go mempool_test.go -run=TestMemPool_Test
```
2. 테스트 함수만 지정
```bash
$ go mod init mempool
$ go test -v -race . -run=TestMemPool_Test
```
3. 전체 테스트 함수 시험
```bash
$ go mod init mempool
$ go test -v -race .
```
