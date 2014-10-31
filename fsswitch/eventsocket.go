/*
go-switch is released under the MIT License <http://www.opensource.org/licenses/mit-license.php
Copyright (C) Temlio Inc. All Rights Reserved.
Provides FreeSWITCH socket communication.
*/
package fsswitch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
	"net/url"
	_ "sort"
	"strconv"
	_ "strings"
)

const eventsBuffer = 16      // For the events channel (memory eater!)
const bufferSize = 1024 << 6 // For the socket reader
type EventSocket struct {
	conn                        net.Conn
	buffer                      *bufio.Reader
	reader                      *bufio.Reader
	textreader                  *textproto.Reader
	eventHandlers               map[string][]func(*Event)
	err                         chan error
	auth, discon, cmd, api, evt chan *Event
}

func NewEventSocket(c net.Conn, evntHandlers map[string][]func(*Event)) *EventSocket {
	socks := EventSocket{
		conn:          c,
		reader:        bufio.NewReaderSize(c, bufferSize),
		eventHandlers: evntHandlers,
		err:           make(chan error),
		cmd:           make(chan *Event),
		api:           make(chan *Event),
		auth:          make(chan *Event),
		discon:        make(chan *Event),
		evt:           make(chan *Event, eventsBuffer),
	}
	socks.textreader = textproto.NewReader(socks.reader)
	return &socks
}

// It's used after parsing plain text event headers, but not JSON.
func copyHeaders(src *textproto.MIMEHeader, dst *Event, decode bool) {
	var err error
	for k, v := range *src {
		//k = capitalize(k)
		if decode {
			dst.Header[k], err = url.QueryUnescape(v[0])
			if err != nil {
				dst.Header[k] = v[0]
			}
		} else {
			dst.Header[k] = v[0]
		}
	}
}

// readOne reads a single event and send over the appropriate channel.
// It separates incoming events from api and command responses.
func (e *EventSocket) readOne() bool {
	log.Println("readOne Start")
	hdr, err := e.textreader.ReadMIMEHeader()
	if err != nil {
		e.err <- err
		log.Println("readOne error reply ")
		return false
	}
	resp := new(Event)
	resp.Header = make(map[string]string)
	if v := hdr.Get("Content-Length"); v != "" {
		length, err := strconv.Atoi(v)
		if err != nil {
			e.err <- err
			return false
		}
		b := make([]byte, length)
		if _, err := io.ReadFull(e.reader, b); err != nil {
			e.err <- err
			return false
		}
		resp.Body = string(b)
	}
	switch hdr.Get("Content-Type") {
	case "command/reply":
		reply := hdr.Get("Reply-Text")
		if reply[0] == '%' {
			copyHeaders(&hdr, resp, true)
		} else {
			copyHeaders(&hdr, resp, false)
		}
		log.Println("readOne command reply ")
		e.cmd <- resp
	case "api/response":
		copyHeaders(&hdr, resp, false)
		log.Printf("readOne api reply : %v", resp.Header)
		e.api <- resp
	case "auth/request":
		copyHeaders(&hdr, resp, false)
		e.auth <- resp
	case "text/event-plain":
		reader := bufio.NewReader(bytes.NewReader([]byte(resp.Body)))
		resp.Body = ""
		textreader := textproto.NewReader(reader)
		hdr, err = textreader.ReadMIMEHeader()
		if err != nil {
			e.err <- err
			return false
		}
		if v := hdr.Get("Content-Length"); v != "" {
			length, err := strconv.Atoi(v)
			if err != nil {
				e.err <- err
				return false
			}
			b := make([]byte, length)
			if _, err = io.ReadFull(reader, b); err != nil {
				e.err <- err
				return false
			}
			resp.Body = string(b)
		}
		copyHeaders(&hdr, resp, true)
		log.Println("readOne event-plain reply ")
		e.evt <- resp
	case "text/event-json":
		tmp := make(EventHeader)
		err := json.Unmarshal([]byte(resp.Body), &tmp)
		if err != nil {
			e.err <- err
			return false
		}
		// capitalize header keys for consistency.
		for k, v := range tmp {
			//resp.Header[capitalize(k)] = v
			resp.Header[k] = v
		}
		if v, _ := resp.Header["_body"]; v != "" {
			resp.Body = v
			delete(resp.Header, "_body")
		} else {
			resp.Body = ""
		}
		log.Println("readOne event-json reply")
		e.evt <- resp
	case "text/disconnect-notice":
		copyHeaders(&hdr, resp, false)
		log.Println("readOne disconnect reply ")
		e.evt <- resp
		return false
	default:
		copyHeaders(&hdr, resp, false)
		log.Println("readOne default :")
		e.evt <- resp
		log.Fatal("Unsupported event:", hdr)
	}
	return true
}

// readLoop calls readOne until a fatal error occurs, then close the socket.
func (e *EventSocket) readLoop() {

	for e.readOne() {

	}
	e.Disconnect()
	//return
}

// ReadEvent reads and returns events from the server. It supports both plain
// or json, but *not* XML.
//
// When subscribing to events (e.g. `Send("events json ALL")`) it makes no
// difference to use plain or json. ReadEvent will parse them and return
// all headers and the body (if any) in an Event struct.
func (e *EventSocket) readEvent() (*Event, error) {
	var (
		ev  *Event
		err error
	)
	select {
	case ev = <-e.discon:
		return nil, errors.New("Disconnected")
	case ev = <-e.evt:
		return ev, nil
	case err = <-e.err:
		return nil, err
	}
}

// Dispatch events to handlers in async mode
func (self *EventSocket) dispatchEvent(event *Event) {
	var eventName string
	if eventName = event.GetHeader("Event-Name", ""); eventName != "" {
		if _, hasHandlers := self.eventHandlers[eventName]; hasHandlers {
			// We have handlers, dispatch to all of them
			for _, handlerFunc := range self.eventHandlers[eventName] {
				go handlerFunc(event)
				return
			}
		}
	}

}

func (e *EventSocket) Connected() bool {
	if e.conn == nil {
		return false
	}
	return true
}

// Disconnects from socket
func (e *EventSocket) Disconnect() (err error) {
	if e.conn != nil {
		err = e.conn.Close()
	}
	return err
}
func (e *EventSocket) ProtocolSendMsg(name, args, uuid string, Lock bool, loop int, asyn bool) (*Event, error) {
	if !e.Connected() {
		return nil, errors.New("Not connected to FS")
	}

	msg := fmt.Sprintf("sendmsg %s\ncall-command: execute\n", uuid)
	msg += fmt.Sprintf("execute-app-name: %s\n", name)
	if Lock {
		msg += fmt.Sprintf("event-lock: %t\n", Lock)
	}
	if loop > 0 {
		msg += fmt.Sprintf("loops: %d\n", loop)
	}
	if asyn {
		msg += fmt.Sprintf("async: %t\n", asyn)
	}
	if args != "" {
		arglen := len(args)
		msg += fmt.Sprintf("content-type: text/plain\ncontent-length: %d\n\n%s\n", arglen, args)
	}
	log.Printf("Sending SendMSG: %s", msg)
	fmt.Fprintf(e.conn, "%s\r\n\r\n", msg)
	var (
		ev *Event
	)
	select {
	case ev = <-e.cmd:
		return ev, nil
	}

}
func (e *EventSocket) ProtocolSend(command, args string) (*Event, error) {
	if !e.Connected() {
		return nil, errors.New("Not connected to FS")
	}
	cmd := fmt.Sprintf("%s %s", command, args)
	fmt.Fprintf(e.conn, "%s\r\n\r\n", cmd)
	var (
		ev *Event
	)
	select {

	case ev = <-e.cmd:
		return ev, nil
	case ev = <-e.api:
		return ev, nil
	}
}
func (e *EventSocket) APICommand(args string) (*Event, error) {
	//"Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#api"
	var evt, err = e.ProtocolSend("api", args)
	return evt, err

}
func (e *EventSocket) BgAPICommand(args string) (*Event, error) {
	//"Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#bgapi"
	var evt, err = e.ProtocolSend("bgapi", args)

	return evt, err
}
func (e *EventSocket) Exit() (*Event, error) {
	//"Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#exit"
	var evt, err = e.ProtocolSend("exit", "")

	return evt, err
}
func (e *EventSocket) Resume() (*Event, error) {
	/* """Socket resume for Outbound connection only.

	   If enabled, the dialplan will resume execution with the next action

	   after the call to the socket application.

	   If there is a bridge active when the disconnect happens, it is killed.*/
	return e.ProtocolSend("resume", "")
}
func (e *EventSocket) ChannelConnect() (*Event, error) {
	//""Socket connect for Outbound connection only.
	return e.ProtocolSend("connect", "")
}
func (e *EventSocket) EventPlain(args string) (*Event, error) {
	//"Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#event"
	return e.ProtocolSend("event plain", args)
}
func (e *EventSocket) EventJson(args string) (*Event, error) {
	//"Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#event"
	return e.ProtocolSend("event json", args)
}
func (e *EventSocket) Event(args string) (*Event, error) {
	//"Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#event"
	return e.ProtocolSend("event", args)
}
func (e *EventSocket) DigitActionSetRealm(args, uuid string, islock bool) (*Event, error) {
	/*Please refer to http://wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_digit_action_set_realm
	  >>> digit_action_set_realm("test1")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("digit_action_set_realm", args, uuid, islock, 0, false)
}
func (e *EventSocket) ClearDigitAction(args, uuid string, islock bool) (*Event, error) {
	/*>>> clear_digit_action("test1")

	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("clear_digit_action", args, uuid, islock, 0, false)
}
func (e *EventSocket) Filter(args string) (*Event, error) {
	/*   "Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#filter

	     The user might pass any number of values to filter an event for. But, from the point
	     filter() is used, just the filtered events will come to the app - this is where this
	     function differs from event().

	     >>> filter('Event-Name MYEVENT')
	     >>> filter('Unique-ID 4f37c5eb-1937-45c6-b808-6fba2ffadb63')
	     """*/
	return e.ProtocolSend("filter", args)
}
func (e *EventSocket) FilterDelete(args string) (*Event, error) {
	/*  "Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#filter_delete

	    >>> filter_delete('Event-Name MYEVENT')
	    """ */
	return e.ProtocolSend("filter delete", args)
}
func (e *EventSocket) DivertEvents(flag string) (*Event, error) {
	/*   "Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#divert_events

	     >>> divert_events("off")
	     >>> divert_events("on")
	     """*/
	return e.ProtocolSend("divert_events", flag)
}
func (e *EventSocket) SendEvent(args string) (*Event, error) {
	/*   "Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#sendevent

	     >>> sendevent("CUSTOM\nEvent-Name; CUSTOM\nEvent-Subclass; myevent;;test\n")

	     This example will send event ;
	       Event-Subclass; myevent%3A%3Atest
	       Command; sendevent%20CUSTOM
	       Event-Name; CUSTOM
	     """ */
	return e.ProtocolSend("sendevent", args)
}
func (e *EventSocket) Auth(args string) (*Event, error) {
	/* "Please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#auth

																																																																																																																																																																																																																																																																						     This method is only used for Inbound connections. */
	return e.ProtocolSend("auth", args)
}
func (e *EventSocket) MyEvent(uuid string) (*Event, error) {
	/*   """For Inbound connection, please refer to http;//wiki.freeswitch.org/wiki/Event_Socket#Special_Case_-_.27myevents.27

	     For Outbound connection, please refer to http;//wiki.freeswitch.org/wiki/Event_Socket_Outbound#Events

	     >>> myevents()

	     For Inbound connection, uuid argument is mandatory.
	     """ */
	return e.ProtocolSend("myevents", uuid)
}
func (e *EventSocket) Linger() (*Event, error) {
	/*   """Tell Freeswitch to wait for the last channel event before ending the connection

	     Can only be used with Outbound connection.

	     >>> linger()

	     """ */
	return e.ProtocolSend("linger", "")
}
func (e *EventSocket) VerboseEvents(uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_verbose_events

	  >>> verbose_events()

	  For Inbound connection, uuid argument is mandatory.
	  """ */
	return e.ProtocolSendMsg("verbose_events", "", uuid, islock, 0, false)
}
func (e *EventSocket) Answer(uuid string, islock bool) (*Event, error) {
	/*   Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_answer

	     >>> answer()

	     For Inbound connection, uuid argument is mandatory.
	     """ */
	return e.ProtocolSendMsg("answer", "", uuid, islock, 0, false)
}
func (e *EventSocket) Bridge(args, uuid string, islock bool) (*Event, error) {
	/*
	   Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_bridge

	   >>> bridge("{ignore_early_media=true}sofia/gateway/myGW/177808")

	   For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("bridge", args, uuid, islock, 0, false)
}
func (e *EventSocket) Hangup(cause, uuid string, islock bool) (*Event, error) {
	/* """Hangup call.

	   Hangup `cause` list ; http;//wiki.freeswitch.org/wiki/Hangup_Causes (Enumeration column)

	   >>> hangup()

	   For Inbound connection, uuid argument is mandatory.
	   """ */
	log.Println("Hanging up")
	return e.ProtocolSendMsg("hangup", cause, uuid, islock, 0, false)
}
func (e *EventSocket) RingReady(cause, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_ring_ready

	  >>> ring_ready()

	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("ring_ready", "", uuid, true, 0, false)
}
func (e *EventSocket) RecordSession(filename, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_record_session
	  >>> record_session("/tmp/dump.gsm")
	  For Inbound connection, uuid argument is mandatory.
	  """ */
	return e.ProtocolSendMsg("record_session", filename, uuid, islock, 0, false)
}
func (e *EventSocket) BindMetaApp(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_bind_meta_app

	  >>> bind_meta_app("2 ab s record_session;;/tmp/dump.gsm")

	  For Inbound connection, uuid argument is mandatory.
	  """ */
	return e.ProtocolSendMsg("bind_meta_app", args, uuid, islock, 0, false)
}
func (e *EventSocket) BindDigitAction(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_bind_digit_action
	  >>> bind_digit_action("test1,456,exec;playback,ivr/ivr-welcome_to_freeswitch.wav")
	  For Inbound connection, uuid argument is mandatory.
	  """ */
	return e.ProtocolSendMsg("bind_digit_action", args, uuid, islock, 0, false)
}
func (e *EventSocket) WaitForSilence(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_wait_for_silence

	  >>> wait_for_silence("200 15 10 5000")

	  For Inbound connection, uuid argument is mandatory.
	  """ */
	return e.ProtocolSendMsg("wait_for_silence", args, uuid, islock, 0, false)
}
func (e *EventSocket) Sleep(milliseconds, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_sleep

	  >>> sleep(5000)
	  >>> sleep("5000")

	  For Inbound connection, uuid argument is mandatory.
	  """*/
	return e.ProtocolSendMsg("sleep", milliseconds, uuid, islock, 0, false)
}
func (e *EventSocket) Vmd(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Mod_vmd
	  >>> vmd("start")
	  >>> vmd("stop")
	  For Inbound connection, uuid argument is mandatory.
	  """ */
	return e.ProtocolSendMsg("vmd", args, uuid, islock, 0, false)
}
func (e *EventSocket) Set(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_set
	  >>> set("ringback=${us-ring}")

	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("set", args, uuid, islock, 0, false)
}
func (e *EventSocket) Export(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_export
	  >>> export("ringback=${us-ring}")

	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("export", args, uuid, islock, 0, false)
}
func (e *EventSocket) SetGlobal(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_set_global
	  >>> set_global("global_var=value")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("set_global", args, uuid, islock, 0, false)
}
func (e *EventSocket) Unset(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_unset
	  >>> unset("ringback")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("unset", args, uuid, islock, 0, false)
}
func (e *EventSocket) StartDtmf(uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_start_dtmf
	  >>> start_dtmf()
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("start_dtmf", "", uuid, islock, 0, false)
}
func (e *EventSocket) StopDtmf(uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_stop_dtmf
	  >>> stop_dtmf()
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("stop_dtmf", "", uuid, islock, 0, false)
}
func (e *EventSocket) StartDtmfGenerate(uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_start_dtmf_generate
	  >>> start_dtmf_generate()
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("start_dtmf_generate", "true", uuid, islock, 0, false)
}
func (e *EventSocket) StopDtmfGenerate(uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_stop_dtmf_generate
	  >>> stop_dtmf_generate()
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("stop_dtmf_generate", "", uuid, islock, 0, false)
}
func (e *EventSocket) QueueDtmf(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_queue_dtmf
	  Enqueue each received dtmf, that'll be sent once the call is bridged.
	  >>> queue_dtmf("0123456789")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("queue_dtmf", args, uuid, islock, 0, false)
}
func (e *EventSocket) FlushDtmf(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_flush_dtmf
	  >>> flush_dtmf()
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("flush_dtmf", "", uuid, islock, 0, false)
}
func (e *EventSocket) PlayFsv(filename, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Mod_fsv
	  >>> play_fsv("/tmp/video.fsv")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("play_fsv", filename, uuid, islock, 0, false)
}
func (e *EventSocket) RecordFsv(filename, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Mod_fsv
	  >>> record_fsv("/tmp/video.fsv")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("record_fsv", filename, uuid, islock, 0, false)
}
func (e *EventSocket) Playback(filename, terminators, uuid string, islock bool, loops int) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Mod_playback
	  The optional argument `terminators` may contain a string withthe characters that will terminate the playback.
	  >>> playback("/tmp/dump.gsm", terminators="#8")
	  In this case, the audio playback is automatically terminated
	  by pressing either '#' or '8'.
	  For Inbound connection, uuid argument is mandatory.
	*/
	if terminators == "" {
		terminators = "none"
	}
	_, _ = e.Set(fmt.Sprintf("playback_terminators=%s", terminators), uuid, true)
	return e.ProtocolSendMsg("playback", filename, uuid, islock, loops, false)
}
func (e *EventSocket) Transfer(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_transfer
	  >>> transfer("3222 XML public Eventault")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("transfer", args, uuid, islock, 0, false)
}
func (e *EventSocket) AttXfer(url, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_att_xfer
	  >>> att_xfer("user/1001")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("att_xfer", url, uuid, islock, 0, false)
}
func (e *EventSocket) EndlessPlayback(filename, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_endless_playback
	  >>> endless_playback("/tmp/dump.gsm")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("endless_playback", filename, uuid, islock, 0, false)
}
func (e *EventSocket) Record(fileName, timeLimit, silenceThresh, silenceHit, terminators, uuid string, loops int) (*Event, error) {
	/*   Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_record

	*/
	if terminators != "" {
		e.Set(fmt.Sprintf("playback_terminators=%s", terminators), uuid, true)
	}
	args := fmt.Sprintf("%s %s %s %s", fileName, timeLimit, silenceThresh, silenceHit)
	return e.ProtocolSendMsg("record", args, uuid, true, loops, false)
}
func (e *EventSocket) PlayAndGetDigits(minDigits, maxDigits, maxTries, timeout int, terminators, invalidFile, digitVarName, validDigits, digitTimeout, uuid string,
	playBeep bool, soundFiles []string) (*Event, error) {
	/*   Please refer to http://wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_play_and_get_digits
	 */
	playStr := ""
	beep := "tone_stream://%(300,200,700)"
	if len(soundFiles) == 0 {
		if playBeep {
			playStr = "tone_stream://%(300,200,700)"
		} else {
			playStr = "silence_stream://10"
		}
	} else {
		e.Set("playback_delimiter=!", uuid, true)
		playStr = "file_string://silence_stream://1"
		for _, soundFile := range soundFiles {
			playStr += fmt.Sprintf("%s!%s", playStr, soundFile)
		}
		if playBeep {
			playStr += fmt.Sprintf("%s!%s", playStr, beep)
		}
	}
	if invalidFile == "" {
		invalidFile = "silence_stream://150"
	}
	if digitTimeout == "" {
		digitTimeout = string(timeout)
	}
	reg := ""
	if validDigits == "" {
		reg = "d+"
	} else {

		byteArray := []byte(validDigits)
		for _, value := range byteArray {
			reg += string(value) + "|"
		}
	}
	regexp := fmt.Sprintf("(%s)", reg)
	args := fmt.Sprintf("%s %s %s %s '%s' %s %s %s %s %s", minDigits, maxDigits, maxTries, timeout, terminators, playStr, invalidFile, digitVarName, regexp, digitTimeout)
	return e.ProtocolSendMsg("play_and_get_digits", args, uuid, true, 0, false)
}
func (e *EventSocket) PreAnswer() (*Event, error) {
	/*  Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_pre_answer

	    Can only be used for outbound connection
	*/
	return e.ProtocolSendMsg("pre_answer", "", "", true, 0, false)
}
func (e *EventSocket) Conference(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Mod_conference
	  >>> conference(args) For Inbound connection, uuid argument is mandatory.*/
	return e.ProtocolSendMsg("conference", args, uuid, islock, 0, false)
}
func (e *EventSocket) Speak(text, uuid string, islock bool, loop int) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/TTS
	  >>> "set" data="tts_engine=flite"
	  >>> "set" data="tts_voice=kal"
	  >>> speak(text)
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("speak", text, uuid, islock, loop, false)
}
func (e *EventSocket) Hupall(args string) (*Event, error) {
	//"Please refer to http;//wiki.freeswitch.org/wiki/Mod_commands#hupall"
	return e.ProtocolSendMsg("hupall", args, "", true, 0, false)
}
func (e *EventSocket) Say(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_say
	  >>> say(en number pronounced 12345)
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("say", args, uuid, islock, 0, false)
}
func (e *EventSocket) SchedHangup(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_sched_hangup

	  >>> sched_hangup("+60 ALLOTTED_TIMEOUT")

	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("sched_hangup", args, uuid, islock, 0, false)
}
func (e *EventSocket) SchedTransfer(args, uuid string, islock bool) (*Event, error) {
	/*"Please refer to http;//wiki.freeswitch.org/wiki/Misc._Dialplan_Tools_sched_transfer
	  >>> sched_transfer("+60 9999 XML public default")
	  For Inbound connection, uuid argument is mandatory.
	*/
	return e.ProtocolSendMsg("sched_transfer", args, uuid, islock, 0, false)
}
