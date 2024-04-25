// Msg Types
//
// All messages received from the gateway are first decoded to the BaseMsg
// type. The BaseMsg type extracts the following JSON from the message:
//		{
//			"janus": <Type>,
//			"transaction": <ID>,
//			"session_id": <Session>,
//			"sender": <Handle>
//		}
// The Type field is inspected to determine which concrete type
// to decode the message to, while the other fields (ID/Session/Handle) are
// inspected to determine where the message should be delivered. Messages
// with an ID field defined are considered responses to previous requests, and
// will be passed directly to requester. Messages without an ID field are
// considered unsolicited events from the gateway and are expected to have
// both Session and Handle fields defined. They will be passed to the Events
// channel of the related Handle and can be read from there.

package janus

var msgtypes = map[string]func() interface{}{
	"error":       func() interface{} { return &ErrorMsg{} },
	"success":     func() interface{} { return &SuccessMsg{} },
	"detached":    func() interface{} { return &DetachedMsg{} },
	"server_info": func() interface{} { return &InfoMsg{} },
	"ack":         func() interface{} { return &AckMsg{} },
	"event":       func() interface{} { return &EventMsg{} },
	"webrtcup":    func() interface{} { return &WebRTCUpMsg{} },
	"media":       func() interface{} { return &MediaMsg{} },
	"hangup":      func() interface{} { return &HangupMsg{} },
	"slowlink":    func() interface{} { return &SlowLinkMsg{} },
	"timeout":     func() interface{} { return &TimeoutMsg{} },
	"destroy":     func() interface{} { return &BaseMsg{} },
}

type BaseMsg struct {
	Type    string `json:"janus"`
	ID      string `json:"transaction"`
	Session uint64 `json:"session_id,omitempty"`
	Handle  uint64 `json:"sender,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
}
type HandlerMessage struct {
	BaseMsg
	Handle uint64         `json:"handle_id,omitempty"`
	Body   map[string]any `json:"body"`
}

type HandlerMessageJsep struct {
	HandlerMessage
	Jsep map[string]any `json:"jsep,omitempty"`
}

type ErrorMsg struct {
	BaseMsg
	Err ErrorData `json:"error"`
}

type ErrorData struct {
	Code   int
	Reason string
}

func (err *ErrorMsg) Error() string {
	return err.Err.Reason
}

type SuccessMsg struct {
	Type string      `json:"janus"`
	ID   string      `json:"transaction"`
	Data SuccessData `json:"data,omitempty"`
}

type SuccessData struct {
	ID uint64 `json:"id"`
}

type DetachedMsg struct{}

type InfoMsg struct {
	Name          string
	Version       int
	VersionString string `json:"version_string"`
	Author        string
	DataChannels  bool   `json:"data_channels"`
	IPv6          bool   `json:"ipv6"`
	LocalIP       string `json:"local-ip"`
	IceTCP        bool   `json:"ice-tcp"`
	Transports    map[string]PluginInfo
	Plugins       map[string]PluginInfo
}

type PluginInfo struct {
	Name          string
	Author        string
	Description   string
	Version       int
	VersionString string `json:"version_string"`
}

type AckMsg struct {
	Type string `json:"janus"`
	ID   string `json:"transaction"`
	Hint string `json:"hint,omitempty"`
}

type TimeoutMsg struct {
	Session uint64 `json:"session_id"`
}

type TrickleOne struct {
	BaseMsg
	HandleR   uint64 `json:"handle_id,omitempty"`
	Candidate any    `json:"candidate"`
}

type TrickleMany struct {
	BaseMsg
	HandleR    uint64 `json:"handle_id,omitempty"`
	Candidates []any  `json:"candidates"`
}

// event types
type EventMsg struct {
	Type       string                 `json:"janus"`
	ID         string                 `json:"transaction"`
	Handle     uint64                 `json:"sender,omitempty"`
	Plugindata PluginData             `json:"plugindata"`
	Jsep       map[string]interface{} `json:"jsep"`
}

type PluginData struct {
	Plugin string                 `json:"plugin"`
	Data   map[string]interface{} `json:"data"`
}
type WebRTCUpMsg struct {
	Type    string `json:"janus"`
	Session uint64 `json:"session_id,omitempty"`
	Handle  uint64 `json:"sender,omitempty"`
}

type SlowLinkMsg struct {
	Type    string `json:"janus"`
	Session uint64 `json:"session_id,omitempty"`
	Handle  uint64 `json:"sender,omitempty"`
	Uplink  bool
	Lost    int64
}

type MediaMsg struct {
	Type      string `json:"janus"`
	Session   uint64 `json:"session_id,omitempty"`
	Handle    uint64 `json:"sender,omitempty"`
	Mid       string `json:"mid"`
	MediaType string `json:"type"`
	Receiving bool   `json:"receiving"`
}

type HangupMsg struct {
	Type    string `json:"janus"`
	Reason  string
	Session uint64 `json:"session_id,omitempty"`
	Handle  uint64 `json:"sender,omitempty"`
}
