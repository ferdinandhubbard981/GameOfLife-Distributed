package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"time"

	"uk.ac.bris.cs/gameoflife/stubs"
)

// initialises bidirectional comms with controller
func (b *Broker) ControllerConnect(req stubs.ConnectRequest, res *stubs.NilResponse) (err error) {
	fmt.Println("Received controller connect request")
	client, dialError := rpc.Dial("tcp", string(req.IP))
	if dialError != nil {
		// err = errors.New("broker > we couldn't dial your IP address of " + string(req.IP))
		println(dialError.Error())
		return dialError
	}
	alreadyExistsError := b.setController(client)
	if alreadyExistsError != nil {
		return alreadyExistsError
	}
	fmt.Println("Controller connected successfully!")
	return
}

func (b *Broker) startWorkers(req stubs.StartGOLRequest) {

	// if controller connects before any workers, block
	for len(b.Workers) == 0 {
		fmt.Println("Waiting for workers to connect")
		time.Sleep(time.Second)
	}
	b.primeWorkers(false) // this is called serially so no mutex
	for _, id := range b.workerIds {
		//run workers
		req := stubs.WorkRequest{
			FlippedCells:   stubs.GetAliveCells(b.getSectionSlice(id)),
			Turns:          b.Params.Turns,
			StartTurn:      b.lastCompletedTurn + 1,
			IsSingleWorker: (len(b.workerIds) < 2),
		}
		b.Workers[id].client.Go(stubs.EvolveSlice, req, new(stubs.NilResponse), nil)
	}
}

// starts the Game of Life Loop
func (b *Broker) StartGOL(req stubs.StartGOLRequest, res *stubs.NilResponse) (err error) {
	// define state to evolve from
	b.lastCompletedTurn = 0
	b.setParams(req.P)
	if req.P.Turns == 0 { //if no turns to be executed, just send back initial state
		b.Controller.Call(stubs.PushState, stubs.PushStateRequest{Turn: 0, FlippedCells: req.InitialAliveCells}, new(stubs.NilResponse))
		return
	}
	b.initialiseWorld(req.InitialAliveCells)

	// start workers
	b.startWorkers(req)
	// check for errors
	lastPushState := time.Now()
	for b.lastCompletedTurn < req.P.Turns && b.Controller != nil {

		select {
		case badWorkerId := <-b.errorChan: //if error: restart workers
			fmt.Printf("R\n")
			// reset all vars
			//remove bad worker with id
			b.resetBroker(badWorkerId)

			b.startWorkers(req)
			lastPushState = time.Now()

		case <-b.processCellsReq:
			fmt.Printf("A\n")
			b.Mu.Lock()
			if b.Controller == nil {
				b.Mu.Unlock()
				continue
			}
			b.lastCompletedTurn++
			//send error if we don't get here within a second
			//update controller
			pushReq := stubs.PushStateRequest{
				FlippedCells: b.flippedCells[b.lastCompletedTurn],
				Turn:         b.lastCompletedTurn,
			}
			b.Controller.Call(stubs.PushState, pushReq, new(stubs.NilResponse))
			//delete entry from maps
			b.applyChanges(b.flippedCells[b.lastCompletedTurn])
			delete(b.workersResponded, b.lastCompletedTurn)
			delete(b.flippedCells, b.lastCompletedTurn)
			lastPushState = time.Now()
			b.Mu.Unlock()
		default:
			time.Sleep(time.Millisecond * 100)
			if time.Now().Second()-lastPushState.Second() > int(time.Second.Seconds()) {
				if b.Controller == nil {
					lastPushState = time.Now()
					continue
				}
				// get workers not responded
				workersNotResponded := b.getWorkersNotResponded()
				for id := range workersNotResponded {
					b.errorChan <- id
				}
			}
			b.Pause.Wait()
		}

	}
	b.primeWorkers(false) //to stop the workers from carrying on executing turns when controller disconnects before all turns have been processed
	b.resetBroker(-1)
	return
}

// kills every worker and halts the broker
func (b *Broker) ServerQuit(req stubs.NilRequest, res *stubs.NilResponse) (err error) {
	b.Pause.Add(1)
	b.killWorkers()
	b.exit = true
	return
}

// remove the controller on voluntary request
func (b *Broker) ControllerQuit(req stubs.NilRequest, res *stubs.NilResponse) (err error) {
	b.removeController()
	fmt.Println("CONTROLLER QUIT")
	return
}

// pauses the game of life loop
func (b *Broker) PauseState(req stubs.NilRequest, res *stubs.NilRequest) (err error) {
	if b.isPaused {
		b.Pause.Done()
		b.isPaused = false
	} else {
		b.Pause.Add(1)
		b.isPaused = true
	}

	for id := range b.workerIds {
		b.Workers[id].client.Call(stubs.WorkerPauseState, new(stubs.NilRequest), new(stubs.NilResponse))
	}
	return
}

func (b *Broker) PushState(req stubs.BrokerPushStateRequest, res *stubs.NilResponse) (err error) {
	fmt.Printf("PushState\n")
	b.Pause.Wait()
	if b.exit || b.Controller == nil {
		return
	}
	b.Mu.Lock()
	defer b.Mu.Unlock()
	_, exists := b.workersResponded[req.Turn]
	if !exists {
		b.workersResponded[req.Turn] = make(map[int]bool)
	}
	b.workersResponded[req.Turn][req.WorkerId] = true
	_, exists = b.flippedCells[req.Turn]
	if exists {
		b.flippedCells[req.Turn] = append(b.flippedCells[req.Turn], req.FlippedCells...)
	} else {
		b.flippedCells[req.Turn] = req.FlippedCells
	}
	if len(b.workersResponded[req.Turn]) == len(b.workerIds) { //if all cells have been processed for this turn
		go func() {
			select {
			case b.processCellsReq <- true: //send cell process request

			}
		}()
	}
	return
}

// connects worker to broker
func (b *Broker) WorkerConnect(req stubs.ConnectRequest, res *stubs.ConnectResponse) (err error) {
	fmt.Println("Received worker connect request")
	client, dialError := rpc.Dial("tcp", string(req.IP))
	if dialError != nil {
		err = errors.New("broker > could not dial IP " + string(req.IP))
	}
	res.Id = b.NextID
	b.addWorker(client, string(req.IP))
	b.primeWorkers(true)
	fmt.Println("Worker connected successfully!")
	return
}

func (b *Broker) WorkerDisconnect(req stubs.RemoveRequest, res *stubs.NilResponse) (err error) {
	b.removeWorkersFromRegister(true, req.Id)
	fmt.Println("removed worker #", req.Id)
	return
}

func main() {
	pAddr := flag.String("port", "9000", "Port to listen on")
	flag.Parse()
	b := NewBroker()
	rpc.Register(b)

	listener, err := net.Listen("tcp", ":"+*pAddr)
	if err != nil {
		fmt.Println("could not listen on port " + *pAddr)
	}
	defer listener.Close()
	go rpc.Accept(listener)
	b.exit = false
	for !b.exit {
		time.Sleep(time.Second)
	}
}
