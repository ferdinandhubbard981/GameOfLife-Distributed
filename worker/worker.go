package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"uk.ac.bris.cs/gameoflife/stubs"
)

var c chan os.Signal

type Worker struct {
	container    stubs.CellsContainer //this will contain only this worker's slice, not the whole world
	rowOffset    int
	id           int
	width        int
	height       int
	topWorker    *rpc.Client
	botWorker    *rpc.Client
	broker       *rpc.Client
	repriming    bool
	topHalo      chan []byte
	botHalo      chan []byte
	exit         bool
	inEvolveLoop bool
	Pause        sync.WaitGroup
	isPaused     bool
}

func (w *Worker) EvolveSlice(req stubs.WorkRequest, res *stubs.NilResponse) (err error) {
	// the worker has been reprimed, so its internal state is empty
	w.container.UpdateWorldAndTurn(req.FlippedCells, 0)
	topDone := make(chan *rpc.Call, 1)
	topDone <- new(rpc.Call)
	botDone := make(chan *rpc.Call, 1)
	botDone <- new(rpc.Call)
	w.repriming = false
	w.inEvolveLoop = true
	for i := req.StartTurn; (i <= req.Turns || req.Turns == -1) && !w.repriming; i++ { //if -1 that means forever
		// send Halo to adjacent workers
		if !req.IsSingleWorker {
			go w.pushHalos(topDone, botDone) //maybe check if they have been received before send next?
		}
		var evolvedSlice [][]byte = createNewSlice(w.height, w.width) // TODO try move this outside of for loop and see if it still works
		// wait for Halo input
		var topHalo []byte = nil
		var botHalo []byte = nil
		if req.IsSingleWorker { //if single worker
			topHalo = w.container.CurrentWorld[w.height-1] //set to last row
			botHalo = w.container.CurrentWorld[0]          //set to first row
		} else { //if more than 1 worker
			exit := false
			for (topHalo == nil || botHalo == nil) && !exit {
				select {
				case topHalo = <-w.topHalo:

				case botHalo = <-w.botHalo:
				default:
					if w.repriming {
						fmt.Println("REPRIMING IS TRUE")
						exit = true
					}
				}
			}

		}
		if w.repriming {
			continue
		}
		// perform iteration
		flipped := w.evolve(evolvedSlice, topHalo, botHalo)
		// updated world in worker api
		w.container.UpdateWorld(evolvedSlice)

		// pushFLippedCells to broker
		brokerReq := stubs.BrokerPushStateRequest{
			FlippedCells: flipped,
			Turn:         i,
			WorkerId:     w.id,
		}
		w.Pause.Wait()
		w.broker.Call(stubs.BrokerPushState, brokerReq, new(stubs.NilResponse))

	}
	w.inEvolveLoop = false
	return
}

func dialWorker(ip string) *rpc.Client {
	client, dialError := rpc.Dial("tcp", string(ip))
	if dialError != nil {
		fmt.Println(dialError)
	} else {
		fmt.Println("no dial error")
	}
	return client
}

func (w *Worker) InitialiseWorker(req stubs.InitWorkerRequest, res *stubs.NilResponse) (err error) {
	w.repriming = true
	for w.inEvolveLoop {
		time.Sleep(time.Millisecond * 100)
	}
	// if using bit masking, then set it to height - 1, width - 1
	if !req.FirstTime {
		stubs.FlushHaloChan(w.topHalo)
		stubs.FlushHaloChan(w.botHalo)
		if w.topWorker != nil && w.botWorker != nil {
			w.topWorker.Close()
			w.botWorker.Close()
		}
	}
	w.width = req.Width
	w.height = req.Height
	fmt.Printf("Worker primed with height: %d & width: %d\n", w.height, w.width)
	w.container.UpdateWorld(createNewSlice(w.height, w.width))
	w.rowOffset = req.RowOffset
	if req.TopWorkerIP != "" {
		w.topWorker = dialWorker(req.TopWorkerIP)
		w.botWorker = dialWorker(req.BotWorkerIP)
	}
	return
}

func (w *Worker) PauseState(req stubs.NilRequest, res *stubs.PauseResponse) (err error) {
	if w.isPaused {
		w.Pause.Done()
		w.isPaused = false
	} else {
		w.Pause.Add(1)
		w.isPaused = true
	}
	return
}

// sent by broker to sleep the distributed system
func (w *Worker) Shutdown(req stubs.NilRequest, res *stubs.NilResponse) (err error) {
	// programmatic Ctrl-C
	defer func() { c <- syscall.SIGINT }()
	w.exit = true
	return
}

func (w *Worker) PushHalo(req stubs.PushHaloRequest, res *stubs.NilResponse) (err error) {

	w.Pause.Wait()
	if req.IsTop {
		w.topHalo <- req.Halo
	} else {
		w.botHalo <- req.Halo
	}

	return
}

func main() {
	bAddr := flag.String("brokerIP", "127.0.0.1:9000", "IP address of broker")
	pAddr := flag.String("port", "9010", "Port to listen on")
	flag.Parse()

	// initalize Worker
	worker := Worker{
		container:    *stubs.NewCellsContainer(),
		topHalo:      make(chan []byte),
		botHalo:      make(chan []byte),
		inEvolveLoop: false,
		Pause:        sync.WaitGroup{},
		isPaused:     false,
	}
	// listen for work
	listener, err := net.Listen("tcp", ":"+*pAddr)
	rpc.Register(&worker)
	if err != nil {
		fmt.Println(err)
	}
	defer listener.Close()
	go rpc.Accept(listener)

	// connect to broker
	broker, _ := rpc.Dial("tcp", *bAddr)
	worker.broker = broker
	res := new(stubs.ConnectResponse)
	req := stubs.ConnectRequest{
		IP: "127.0.0.1:" + *pAddr,
	}
	broker.Call(stubs.WorkerConnect, req, res)
	worker.id = res.Id

	// detect Ctrl-C
	c = make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	for !worker.exit {
		select {
		case <-c:
			broker.Call(stubs.WorkerDisconnect, stubs.RemoveRequest{Id: worker.id}, new(stubs.NilResponse))
			worker.exit = true
		default:
			time.Sleep(time.Second)
		}
	}
}
