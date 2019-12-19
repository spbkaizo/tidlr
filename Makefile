all:
	go build -v -x -o tidlr-freebsd12-amd64
	GOOS=linux GOARCH=amd64 go build -v -x -o tidlr-linux-amd64
	GOOS=windows go build -v -x -o tidlr-windows.exe


clean:
	go clean -a -v -x
