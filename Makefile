all:
	go build -v -x

linux:
	GOOS=linux GOARCH=amd64 go build -v -x -o tidlr-linux-amd64
