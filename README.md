# Commands
## Benchmark:
go run server.go
then
In separate terminal:
go test -run=^$ -bench ^BenchmarkLocal$ -benchtime=1x > benchmark/benchmark.txt
cd benchmark
python3 plot.py

### results

![](benchmark/16x16.jpg)
![](benchmark/64x64.jpg)
![](benchmark/128x128.jpg)
![](benchmark/256x256.jpg)
![](benchmark/512x512.jpg)

scp -P 17282 ferdi@6.tcp.eu.ngrok.io:~/distdownload.zip .
go run . -port 9010 -thisIp  -brokerIP :9000