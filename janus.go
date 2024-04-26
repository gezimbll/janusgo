// Package janus is a Golang implementation of the Janus API, used to interact
// with the Janus WebRTC Gateway.
package janus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"
)

var debug = false

func unexpected(request string) error {
	return fmt.Errorf("Unexpected response received to '%s' request", request)
}

func newRequest(method string) (map[string]interface{}, chan interface{}) {
	req := make(map[string]interface{}, 8)
	req["janus"] = method
	return req, make(chan interface{})
}

// Gateway represents a connection to an instance of the Janus Gateway.
type Gateway struct {
	// Sessions is a map of the currently active sessions to the gateway.
	Sessions map[uint64]*Session

	// Access to the Sessions map should be synchronized with the Gateway.Lock()
	// and Gateway.Unlock() methods provided by the embeded sync.Mutex.
	sync.Mutex

	conn             *websocket.Conn
	transactions     map[string]chan interface{}
	transactionsUsed map[string]bool
	errors           chan error
	sendChan         chan []byte
	writeMu          sync.Mutex
}

// Connect initiates a webscoket connection with the Janus Gateway
func Connect(wsURL string) (*Gateway, error) {

	conn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		Subprotocols: []string{"janus-protocol"},
	})

	if err != nil {
		return nil, err
	}

	gateway := new(Gateway)
	gateway.conn = conn
	gateway.transactions = make(map[string]chan interface{})
	gateway.transactionsUsed = make(map[string]bool)
	gateway.Sessions = make(map[uint64]*Session)
	gateway.sendChan = make(chan []byte, 100)
	gateway.errors = make(chan error)

	go gateway.ping()
	go gateway.recv()
	return gateway, nil
}

// Close closes the underlying connection to the Gateway.
func (gateway *Gateway) Close() error {
	return gateway.conn.Close(websocket.StatusNormalClosure, "")
}

// GetErrChan returns a channels through which the caller can check and react to connectivity errors
func (gateway *Gateway) GetErrChan() chan error {
	return gateway.errors
}

func (gateway *Gateway) send(msg any, transactionID string, transaction chan interface{}) {

	gateway.Lock()
	gateway.transactions[transactionID] = transaction
	gateway.transactionsUsed[transactionID] = false
	gateway.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("json.Marshal: %s\n", err)
		return
	}

	if debug {
		// log message being sent
		var log bytes.Buffer
		json.Indent(&log, data, ">", "   ")
		log.Write([]byte("\n"))
		log.WriteTo(os.Stdout)
	}

	gateway.writeMu.Lock()
	err = gateway.conn.Write(context.Background(), websocket.MessageText, data)
	gateway.writeMu.Unlock()

	if err != nil {
		select {
		case gateway.errors <- err:
		default:
			fmt.Printf("conn.Write: %s\n", err)
		}

		return
	}
}

func passMsg(ch chan interface{}, msg interface{}) {
	ch <- msg
}

func (gateway *Gateway) ping() {
	ticker := time.NewTicker(time.Second * 30)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			err := gateway.conn.Ping(context.Background())
			if err != nil {
				select {
				case gateway.errors <- err:
				default:
					log.Println("ping:", err)
				}

				return
			}
		}
	}
}

func (gateway *Gateway) sendloop() {

}

func (gateway *Gateway) recv() {

	for {
		// Read message from Gateway

		// Decode to Msg struct
		var base BaseMsg

		_, data, err := gateway.conn.Read(context.Background())
		if err != nil {
			select {
			case gateway.errors <- err:
			default:
				fmt.Printf("conn.Read: %s\n", err)
			}

			return
		}

		if err := json.Unmarshal(data, &base); err != nil {
			fmt.Printf("json.Unmarshal: %s\n", err)
			continue
		}

		if debug {
			// log message being sent
			var log bytes.Buffer
			json.Indent(&log, data, "<", "   ")
			log.Write([]byte("\n"))
			log.WriteTo(os.Stdout)
		}

		typeFunc, ok := msgtypes[base.Type]
		if !ok {
			fmt.Printf("Unknown message type received!\n")
			continue
		}

		msg := typeFunc()
		if err := json.Unmarshal(data, &msg); err != nil {
			fmt.Printf("json.Unmarshal: %s\n", err)
			continue // Decode error
		}

		var transactionUsed bool
		if base.ID != "" {
			id := base.ID
			gateway.Lock()
			transactionUsed = gateway.transactionsUsed[id]
			gateway.Unlock()

		}

		// Pass message on from here
		if base.ID == "" || transactionUsed {
			// Is this a Handle event?
			if base.Handle == 0 {
				// Error()
			} else {
				// Lookup Session
				gateway.Lock()
				session := gateway.Sessions[base.Session]
				gateway.Unlock()
				if session == nil {
					fmt.Printf("Unable to deliver message. Session gone?\n")
					continue
				}

				// Lookup Handle
				session.Lock()
				handle := session.Handles[base.Handle]
				session.Unlock()
				if handle == nil {
					fmt.Printf("Unable to deliver message. Handle gone?\n")
					continue
				}

				// Pass msg
				go passMsg(handle.Events, msg)
			}
		} else {
			// Lookup Transaction
			gateway.Lock()
			transaction := gateway.transactions[base.ID]
			switch msg.(type) {
			case *EventMsg:
				gateway.transactionsUsed[base.ID] = true
			}
			gateway.Unlock()
			if transaction == nil {
				// Error()
			}

			// Pass msg
			go passMsg(transaction, msg)
		}
	}
}

// Info sends an info request to the Gateway.
// On success, an InfoMsg will be returned and error will be nil.
func (gateway *Gateway) Info(ctx context.Context, msg BaseMsg) (*InfoMsg, error) {
	ch := make(chan any)
	gateway.send(msg, msg.ID, ch)

	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case *InfoMsg:
			return msg, nil
		case *ErrorMsg:
			return nil, msg
		default:
			return nil, nil
		}
	case <-ctx.Done():
		gateway.transactions[msg.ID] = nil
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())

	}
}

// Create sends a create request to the Gateway.
// On success, a new Session will be returned and error will be nil.
func (gateway *Gateway) CreateSession(ctx context.Context, msg BaseMsg) (*SuccessMsg, error) {
	ch := make(chan any)
	gateway.send(msg, msg.ID, ch)

	select {
	case response := <-ch:
		switch response := response.(type) {
		case *SuccessMsg:
			session := new(Session)
			session.ID = response.Data.ID
			session.Handles = make(map[uint64]*Handle)
			session.gateway = gateway
			gateway.Lock()
			gateway.Sessions[session.ID] = session
			gateway.Unlock()
			return response, nil
		case *ErrorMsg:
			return nil, response
		default:
			return nil, nil

		}
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}
}

// Session represents a session instance on the Janus Gateway.
type Session struct {
	// ID is the session_id of this session
	ID uint64

	// Handles is a map of plugin handles within this session
	Handles map[uint64]*Handle

	Events chan interface{}

	// Access to the Handles map should be synchronized with the Session.Lock()
	// and Session.Unlock() methods provided by the embeded sync.Mutex.
	sync.Mutex

	gateway *Gateway
}

func (session *Session) send(msg any, transactionID string, transaction chan interface{}) {
	session.gateway.send(msg, transactionID, transaction)
}

// Attach sends an attach request to the Gateway within this session.
// plugin should be the unique string of the plugin to attach to.
// On success, a new Handle will be returned and error will be nil.
func (session *Session) AttachSession(ctx context.Context, msg BaseMsg) (*SuccessMsg, error) {
	ch := make(chan any)
	session.send(msg, msg.ID, ch)

	select {
	case response := <-ch:
		switch res := response.(type) {
		case *SuccessMsg:
			handle := new(Handle)
			handle.session = session
			handle.ID = res.Data.ID
			handle.Events = make(chan interface{}, 8)
			session.Lock()
			session.Handles[handle.ID] = handle
			session.Unlock()
			return res, nil
		case *ErrorMsg:

			return nil, res
		default:
			return nil, nil

		}
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())

	}

}

// KeepAlive sends a keep-alive request to the Gateway.
// On success, an AckMsg will be returned and error will be nil.
func (session *Session) KeepAlive(ctx context.Context, msg BaseMsg) (*AckMsg, error) {
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	ch := make(chan any)
	session.send(msg, msg.ID, ch)

	select {
	case res := <-ch:
		switch msg := res.(type) {
		case *AckMsg:
			return msg, nil
		default:
			return nil, nil
		}

	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}

}

// Destroy sends a destroy request to the Gateway to tear down this session.
// On success, the Session will be removed from the Gateway.Sessions map, an
// AckMsg will be returned and error will be nil.
func (session *Session) DestroySession(ctx context.Context, msg BaseMsg) (*AckMsg, error) {
	ch := make(chan any)
	session.send(msg, msg.ID, ch)

	select {
	case response := <-ch:

		switch response := response.(type) {
		case *AckMsg:

			session.gateway.Lock()
			delete(session.gateway.Sessions, session.ID)
			session.gateway.Unlock()

			return response, nil
		default:
			return nil, nil

		}

	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}

}

// Handle represents a handle to a plugin instance on the Gateway.
type Handle struct {
	// ID is the handle_id of this plugin handle
	ID uint64

	// Type   // pub  or sub
	Type string

	//User   // Userid
	User string

	// Events is a receive only channel that can be used to receive events
	// related to this handle from the gateway.
	Events chan interface{}

	session *Session
}

func (handle *Handle) send(msg any, transactionID string, transaction chan interface{}) {
	handle.session.send(msg, transactionID, transaction)
}

// Request sends a sync request
func (handle *Handle) Request(ctx context.Context, msg HandlerMessage) (*SuccessMsg, error) {
	ch := make(chan any)

	handle.send(msg, msg.ID, ch)

	select {
	case res := <-ch:
		switch res := res.(type) {
		case *SuccessMsg:
			return res, nil
		case *ErrorMsg:
			return nil, res
		default:
			return nil, nil
		}

	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}

}

// Message sends a message request to a plugin handle on the Gateway.
// body should be the plugin data to be passed to the plugin, and jsep should
// contain an optional SDP offer/answer to establish a WebRTC PeerConnection.
// On success, an EventMsg will be returned and error will be nil.
func (handle *Handle) Message(ctx context.Context, msg HandlerMessageJsep) (*EventMsg, error) {
	ch := make(chan any)

	handle.send(msg, msg.ID, ch)

	select {
	case msg := <-ch:
		// No tears..
	GetMessage:
		switch msg := msg.(type) {
		case *AckMsg:
			goto GetMessage // ..only dreams.
		case *EventMsg:
			return msg, nil
		case *ErrorMsg:
			return nil, msg
		default:
			return nil, nil
		}

	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}

}

// Trickle sends a trickle request to the Gateway as part of establishing
// a new PeerConnection with a plugin.
// candidate should be a single ICE candidate, or a completed object to
// signify that all candidates have been sent:
//
//	{
//		"completed": true
//	}
//
// On success, an AckMsg will be returned and error will be nil.
func (handle *Handle) Trickle(ctx context.Context, msg TrickleOne) (*AckMsg, error) {
	ch := make(chan any)
	handle.send(msg, msg.ID, ch)

	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case *AckMsg:
			return msg, nil
		case *ErrorMsg:
			return nil, msg
		default:
			return nil, nil
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}

}

// TrickleMany sends a trickle request to the Gateway as part of establishing
// a new PeerConnection with a plugin.
// candidates should be an array of ICE candidates.
// On success, an AckMsg will be returned and error will be nil.
func (handle *Handle) TrickleMany(ctx context.Context, msg TrickleMany) (*AckMsg, error) {
	ch := make(chan any)
	handle.send(msg, msg.ID, ch)

	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case *AckMsg:
			return msg, nil
		case *ErrorMsg:
			return nil, msg
		default:
			return nil, nil
		}

	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}
}

// Detach sends a detach request to the Gateway to remove this handle.
// On success, an AckMsg will be returned and error will be nil.
func (handle *Handle) Detach(ctx context.Context, msg BaseMsg) (*AckMsg, error) {
	ch := make(chan any)
	handle.send(msg, msg.ID, ch)

	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case *AckMsg:
			handle.session.Lock()
			delete(handle.session.Handles, handle.ID)
			handle.session.Unlock()
			return msg, nil
		case *ErrorMsg:
			return nil, msg
		default:
			return nil, nil
		}

	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response %w", ctx.Err())
	}

}
