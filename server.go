package canopus

import (
	"bytes"
	"log"
	"net"
	"strconv"
	"time"
)

func NewLocalServer() *CoapServer {
	localAddr, err := net.ResolveUDPAddr("udp6", ":5683")
	if err != nil {
		log.Fatal("Error starting CoAP Server: ", err)
		return nil
	}
	return NewServer(localAddr, nil)
}

func NewCoapServer(local string) *CoapServer {
	localAddr, _ := net.ResolveUDPAddr("udp", local)

	return NewServer(localAddr, nil)
}

func NewServer(localAddr *net.UDPAddr, remoteAddr *net.UDPAddr) *CoapServer {
	return &CoapServer{
		remoteAddr:            remoteAddr,
		localAddr:             localAddr,
		events:                NewCanopusEvents(),
		observations:          make(map[string][]*Observation),
		fnHandleCoapCoapProxy: NullProxyHandler,
		fnHandleCoapHttpProxy: NullProxyHandler,
		queue: NewDefaultQueue(),
	}
}

type CoapServer struct {
	localAddr  *net.UDPAddr
	remoteAddr *net.UDPAddr

	localConn  *net.UDPConn
	remoteConn *net.UDPConn

	messageIds   map[uint16]time.Time
	routes       []*Route
	events       *CanopusEvents
	observations map[string][]*Observation

	fnHandleCoapHttpProxy ProxyHandler
	fnHandleCoapCoapProxy ProxyHandler

	queue Queue
}

func (s *CoapServer) Start() {

	var discoveryRoute RouteHandler = func(req CoapRequest) CoapResponse {
		msg := req.GetMessage()

		ack := ContentMessage(msg.MessageId, TYPE_ACKNOWLEDGEMENT)
		ack.AddOption(OPTION_CONTENT_FORMAT, MEDIATYPE_APPLICATION_LINK_FORMAT)

		var buf bytes.Buffer
		for _, r := range s.routes {
			if r.Path != ".well-known/core" {
				buf.WriteString("</")
				buf.WriteString(r.Path)
				buf.WriteString(">")

				// Media Types
				lenMt := len(r.MediaTypes)
				if lenMt > 0 {
					buf.WriteString(";ct=")
					for idx, mt := range r.MediaTypes {

						buf.WriteString(strconv.Itoa(int(mt)))
						if idx+1 < lenMt {
							buf.WriteString(" ")
						}
					}
				}

				buf.WriteString(",")
				// buf.WriteString("</" + r.Path + ">;ct=0,")
			}
		}
		ack.Payload = NewPlainTextPayload(buf.String())

		resp := NewResponseWithMessage(ack)

		return resp
	}

	s.NewRoute("/.well-known/core", GET, discoveryRoute)
	s.serveServer()
}

func (s *CoapServer) serveServer() {
	s.messageIds = make(map[uint16]time.Time)

	conn, err := net.ListenUDP("udp", s.localAddr)
	if err != nil {
		s.events.Error(err)
		log.Fatal(err)
	}

	s.localConn = conn

	if conn == nil {
		log.Fatal("An error occured starting up CoAP Server")
	} else {
		log.Println("Started CoAP Server ", conn.LocalAddr())
	}

	s.events.Started(s)

	s.handleMessageIdPurge()
	s.queue.Start()

	readBuf := make([]byte, BUF_SIZE)
	for {
		len, addr, err := conn.ReadFromUDP(readBuf)

		if err == nil {

			msgBuf := make([]byte, len)
			copy(msgBuf, readBuf)

			go s.handleMessage(msgBuf, conn, addr)
		}
	}
}

func (s *CoapServer) Stop() {
	s.localConn.Close()
}

func (s *CoapServer) handleMessageIdPurge() {
	// Routine for clearing up message IDs which has expired
	ticker := time.NewTicker(MESSAGEID_PURGE_DURATION * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				for k, v := range s.messageIds {
					elapsed := time.Since(v)
					if elapsed > MESSAGEID_PURGE_DURATION {
						delete(s.messageIds, k)
					}
				}
			}
		}
	}()
}

func (s *CoapServer) handleMessage(msgBuf []byte, conn *net.UDPConn, addr *net.UDPAddr) {
	msg, err := BytesToMessage(msgBuf)
	s.events.Message(msg, true)

	if msg.MessageType == TYPE_ACKNOWLEDGEMENT {
		if msg.GetOption(OPTION_OBSERVE) != nil {

			s.events.Notify(msg.GetUriPath(), msg.Payload, msg)
			return
		}
	} else {
		if msg.MessageType != TYPE_RESET {
			// Unsupported Method
			if msg.Code != GET && msg.Code != POST && msg.Code != PUT && msg.Code != DELETE {
				resp := NotImplementedMessage(msg.MessageId, TYPE_ACKNOWLEDGEMENT)
				resp.CloneOptions(msg, OPTION_URI_PATH, OPTION_CONTENT_FORMAT)

				s.events.Message(resp, false)
				SendMessageTo(resp, NewCanopusUDPConnection(conn), addr)
				return
			}

			if err != nil {
				s.events.Error(err)
				if err == ERR_UNKNOWN_CRITICAL_OPTION {
					if msg.MessageType == TYPE_CONFIRMABLE {
						SendMessageTo(BadOptionMessage(msg.MessageId, TYPE_ACKNOWLEDGEMENT), NewCanopusUDPConnection(conn), addr)
						return
					} else {
						// Ignore silently
						return
					}
				}
			}

			// Proxy
			if IsProxyRequest(msg) {
				if IsCoapUri(msg) {
					s.fnHandleCoapCoapProxy(msg, conn, addr)
				} else if IsHttpUri(msg.GetOption(OPTION_PROXY_URI).StringValue()) {
					s.fnHandleCoapHttpProxy(msg, conn, addr)
				} else {
					// Unknown URI
				}
			} else {
				route, attrs, err := MatchingRoute(msg.GetUriPath(), MethodString(msg.Code), msg.GetOptions(OPTION_CONTENT_FORMAT), s.routes)
				if err != nil {
					if err == ERR_NO_MATCHING_ROUTE {
						ret := NotFoundMessage(msg.MessageId, TYPE_ACKNOWLEDGEMENT)
						ret.CloneOptions(msg, OPTION_URI_PATH, OPTION_CONTENT_FORMAT)
						ret.Token = msg.Token

						s.events.Message(ret, false)
						SendMessageTo(ret, NewCanopusUDPConnection(conn), addr)

						s.events.Error(err)
						return
					}

					if err == ERR_NO_MATCHING_METHOD {
						ret := MethodNotAllowedMessage(msg.MessageId, TYPE_ACKNOWLEDGEMENT)
						ret.CloneOptions(msg, OPTION_URI_PATH, OPTION_CONTENT_FORMAT)

						s.events.Message(ret, false)
						SendMessageTo(ret, NewCanopusUDPConnection(conn), addr)

						s.events.Error(err)
						return
					}

					if err == ERR_UNSUPPORTED_CONTENT_FORMAT {
						ret := UnsupportedContentFormatMessage(msg.MessageId, TYPE_ACKNOWLEDGEMENT)
						ret.CloneOptions(msg, OPTION_URI_PATH, OPTION_CONTENT_FORMAT)

						s.events.Message(ret, false)
						SendMessageTo(ret, NewCanopusUDPConnection(conn), addr)

						s.events.Error(err)
						return
					}
				}

				// Duplicate Message ID Check
				_, dupe := s.messageIds[msg.MessageId]
				if dupe {
					log.Println("Duplicate Message ID ", msg.MessageId)
					if msg.MessageType == TYPE_CONFIRMABLE {
						ret := EmptyMessage(msg.MessageId, TYPE_RESET)
						ret.CloneOptions(msg, OPTION_URI_PATH, OPTION_CONTENT_FORMAT)

						s.events.Message(ret, false)
						SendMessageTo(ret, NewCanopusUDPConnection(conn), addr)
					}
					return
				}

				if err == nil {
					s.messageIds[msg.MessageId] = time.Now()

					// Auto acknowledge
					if msg.MessageType == TYPE_CONFIRMABLE && route.AutoAck {
						ack := NewMessageOfType(TYPE_ACKNOWLEDGEMENT, msg.MessageId)

						s.events.Message(ack, false)
						SendMessageTo(ack, NewCanopusUDPConnection(conn), addr)
					}

					req := NewClientRequestFromMessage(msg, attrs, conn, addr)

					if msg.MessageType == TYPE_CONFIRMABLE {
						obsOpt := msg.GetOption(OPTION_OBSERVE)
						if obsOpt != nil {
							// TODO: if server doesn't allow observing, return error

							if obsOpt.Value == nil {
								// TODO: Check if observation has been registered, if yes, remove it (observation == cancel)
								resource := msg.GetUriPath()
								if s.hasObservation(resource, addr) {
									// Remove observation of client
									s.removeObservation(resource, addr)

									// Observe Cancel Request & Fire OnObserveCancel Event
									s.events.ObserveCancelled(resource, msg)
								} else {
									// Register observation of client
									s.addObservation(msg.GetUriPath(), string(msg.Token), addr)

									// Observe Request & Fire OnObserve Event
									s.events.Observe(resource, msg)
								}

								req.GetMessage().AddOption(OPTION_OBSERVE, 1)
							}
						}
					}

					resp := route.Handler(req)
					_, nilresponse := resp.(NilResponse)
					if !nilresponse {
						respMsg := resp.GetMessage()

						// TODO: Validate Message before sending (e.g missing messageId)
						err := ValidateMessage(respMsg)
						if err == nil {
							s.events.Message(respMsg, false)
							SendMessageTo(respMsg, NewCanopusUDPConnection(conn), addr)
						}
					}
				}
			}
		}
	}
}

func (s *CoapServer) Get(path string, fn RouteHandler) *Route {
	return s.add(METHOD_GET, path, fn)
}

func (s *CoapServer) Delete(path string, fn RouteHandler) *Route {
	return s.add(METHOD_DELETE, path, fn)
}

func (s *CoapServer) Put(path string, fn RouteHandler) *Route {
	return s.add(METHOD_PUT, path, fn)
}

func (s *CoapServer) Post(path string, fn RouteHandler) *Route {
	return s.add(METHOD_POST, path, fn)
}

func (s *CoapServer) Options(path string, fn RouteHandler) *Route {
	return s.add(METHOD_OPTIONS, path, fn)
}

func (s *CoapServer) Patch(path string, fn RouteHandler) *Route {
	return s.add(METHOD_PATCH, path, fn)
}

func (s *CoapServer) add(method string, path string, fn RouteHandler) *Route {
	route := CreateNewRoute(path, method, fn)
	s.routes = append(s.routes, route)

	return route
}

func (s *CoapServer) NewRoute(path string, method CoapCode, fn RouteHandler) *Route {
	route := CreateNewRoute(path, MethodString(method), fn)
	s.routes = append(s.routes, route)

	return route
}

func (c *CoapServer) Send(req CoapRequest) (CoapResponse, error) {
	c.events.Message(req.GetMessage(), false)
	response, err := SendMessageTo(req.GetMessage(), NewCanopusUDPConnection(c.localConn), c.remoteAddr)

	if err != nil {
		c.events.Error(err)
		return response, err
	}
	c.events.Message(response.GetMessage(), true)

	return response, err
}

func (c *CoapServer) SendTo(req CoapRequest, addr *net.UDPAddr) (CoapResponse, error) {
	return SendMessageTo(req.GetMessage(), NewCanopusUDPConnection(c.localConn), addr)
}

func (c *CoapServer) NotifyChange(resource, value string, confirm bool) {
	t := c.observations[resource]

	if t != nil {
		var req CoapRequest

		if confirm {
			req = NewRequest(TYPE_CONFIRMABLE, COAPCODE_205_CONTENT, GenerateMessageId())
		} else {
			req = NewRequest(TYPE_ACKNOWLEDGEMENT, COAPCODE_205_CONTENT, GenerateMessageId())
		}

		for _, r := range t {
			req.SetToken(r.Token)
			req.SetStringPayload(value)
			req.SetRequestURI(r.Resource)
			r.NotifyCount++
			req.GetMessage().AddOption(OPTION_OBSERVE, r.NotifyCount)

			go c.SendTo(req, r.Addr)
		}
	}
}

func (s *CoapServer) addObservation(resource, token string, addr *net.UDPAddr) {
	s.observations[resource] = append(s.observations[resource], NewObservation(addr, token, resource))
}

func (s *CoapServer) hasObservation(resource string, addr *net.UDPAddr) bool {
	obs := s.observations[resource]
	if obs == nil {
		return false
	}

	for _, o := range obs {
		if o.Addr.String() == addr.String() {
			return true
		}
	}
	return false
}

func (s *CoapServer) removeObservation(resource string, addr *net.UDPAddr) {
	obs := s.observations[resource]
	if obs == nil {
		return
	}

	for idx, o := range obs {
		if o.Addr.String() == addr.String() {
			s.observations[resource] = append(obs[:idx], obs[idx+1:]...)
			return
		}
	}
}

func (c *CoapServer) Dial(host string) {
	remoteAddr, _ := net.ResolveUDPAddr("udp6", host)

	c.remoteAddr = remoteAddr
}

func (c *CoapServer) Dial6(host string) {
	remoteAddr, _ := net.ResolveUDPAddr("udp6", host)

	c.remoteAddr = remoteAddr
}

func (s *CoapServer) OnNotify(fn FnEventNotify) {
	s.events.OnNotify(fn)
}

func (s *CoapServer) OnStart(fn FnEventStart) {
	s.events.OnStart(fn)
}

func (s *CoapServer) OnClose(fn FnEventClose) {
	s.events.OnClose(fn)
}

func (s *CoapServer) OnDiscover(fn FnEventDiscover) {
	s.events.OnDiscover(fn)
}

func (s *CoapServer) OnError(fn FnEventError) {
	s.events.OnError(fn)
}

func (s *CoapServer) OnObserve(fn FnEventObserve) {
	s.events.OnObserve(fn)
}

func (s *CoapServer) OnObserveCancel(fn FnEventObserveCancel) {
	s.events.OnObserveCancel(fn)
}

func (s *CoapServer) OnMessage(fn FnEventMessage) {
	s.events.OnMessage(fn)
}

type ProxyType int

const (
	PROXY_COAP_HTTP ProxyType = 0
	PROXY_COAP_COAP ProxyType = 1
)

func (s *CoapServer) SetProxy(t ProxyType, enabled bool) {
	if t == PROXY_COAP_HTTP {
		if enabled {
			s.fnHandleCoapHttpProxy = CoapHttpProxyHandler
		} else {
			s.fnHandleCoapHttpProxy = NullProxyHandler
		}
	} else if t == PROXY_COAP_COAP {
		if enabled {
			s.fnHandleCoapCoapProxy = CoapHttpProxyHandler
		} else {
			s.fnHandleCoapCoapProxy = NullProxyHandler
		}
	}
}

////////////////////////////////////////////////////////////////////////////////
func NewObservation(addr *net.UDPAddr, token string, resource string) *Observation {
	return &Observation{
		Addr:        addr,
		Token:       token,
		Resource:    resource,
		NotifyCount: 0,
	}
}

type Observation struct {
	Addr        *net.UDPAddr
	Token       string
	Resource    string
	NotifyCount int
}
