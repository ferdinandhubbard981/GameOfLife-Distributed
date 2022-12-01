package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"syscall"

	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

var c chan os.Signal
type Worker struct {
	container      stubs.CellsContainer
	height 	       int
	width          int
	widthBitmask   int
	heightBitmask  int
	offset         int
	id 			   int
}

func (w *Worker) EvolveSlice(req stubs.WorkRequest, res *stubs.WorkResponse) (err error) {
	// 1    worker GOL reprimed = no halos + body 
	// multiworker GOL reprimed = halos    + body
	// 1    worker GOL noprime  = no halos + no body
	// multiworker GOL noprime  = halos    + no body
	if req.FlippedCells != nil {
		// the worker has been reprimed, so its internal state is empty
		w.container.UpdateWorld(req.FlippedCells, 0) // TODO: change so that worker keeps track of own turn
	}
	
	var flipped []util.Cell
	var evolvedSlice [][]byte = createNewSlice(w.height, w.width)
	if req.BottomHalo == nil {
		// single worker game of life
		fmt.Println("running single worker GOL")
		w.container.Mu.Lock()
		flipped = w.singleWorkerGOL(evolvedSlice)
		w.container.Mu.Unlock()
		// find the difference between old and new, send back to broker
		w.container.InitialiseWorld(evolvedSlice)
		res.FlippedCells = flipped
		return
	}
	// multi-worker game of life
	fmt.Println("running multi-worker GOL")
	topHalo := stubs.ConstructHalo(req.TopHalo, w.width)
	bottomHalo := stubs.ConstructHalo(req.BottomHalo, w.width)

	w.container.Mu.Lock()
	flipped = w.multiWorkerGOL(evolvedSlice, topHalo, bottomHalo)
	w.container.Mu.Unlock()
	w.container.InitialiseWorld(evolvedSlice)
	res.FlippedCells = flipped
	return
}

func (w *Worker) InitialiseWorker(req stubs.InitWorkerRequest, res *stubs.NilResponse) (err error) {
	// if using bit masking, then set it to height - 1, width - 1
	w.heightBitmask = req.Height - 1
	w.widthBitmask = req.Width - 1

	fmt.Println("Worker primed with height-width ", req.Height, "-", req.Width)
	w.height = req.Height
	w.width = req.Width
	w.container.InitialiseWorld(createNewSlice(req.Height, req.Width))
	w.offset = req.WorkerIndex * w.height
	return
}

// sent by broker to sleep the distributed system
func (w *Worker) Shutdown(req stubs.NilRequest, res *stubs.NilResponse) (err error) {
	// programmatic Ctrl-C
	defer func(){c <- syscall.SIGINT}()
	return
}

func main() {
	bAddr := flag.String("brokerIP", "127.0.0.1:9000", "IP address of broker")
	pAddr := flag.String("port", "9010", "Port to listen on")
    flag.Parse()
	
	// listen for work
    listener, err := net.Listen("tcp", ":" + *pAddr)
	worker := Worker{container: *stubs.NewCellsContainer()}
	rpc.Register(&worker)
	if err != nil {
		fmt.Println(err)
	}
    defer listener.Close()
    go rpc.Accept(listener)

	// connect to broker
	client, _ := rpc.Dial("tcp", *bAddr)
	res := new(stubs.ConnectResponse)
	req := stubs.ConnectRequest{
		IP: stubs.IPAddress("127.0.0.1:" + *pAddr),
	}
	client.Call(stubs.WorkerConnect, req, res)
	worker.id = res.Id

	// detect Ctrl-C
	c = make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	client.Call(stubs.WorkerDisconnect, stubs.RemoveRequest{Id: worker.id}, new(stubs.NilResponse))
}