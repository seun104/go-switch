package main

import (
	"github.com/temlioinc/go-switch"
	"log"
)

type OutboundManager struct {
	*fsswitch.OutboundSocket
}

func NewOutboundManager(client *fsswitch.OutboundSocket) {
	log.Println("NewManager")
	outbound := new(OutboundManager)
	outbound.OutboundSocket = client
	outbound.Connect(outbound.createHandlers(), false)
	go outbound.Start()
	outbound.Run()

	//outbound.Start()

}
func (self *OutboundManager) Run() {
	audioFile := "C:/test.wav"
	//time.Sleep(5 * time.Second)
	//self.Channel.PrettyPrint()
	//self.MyEvent("")
	////if err != nil {
	////	log.Println(err)
	////}
	//ev.PrettyPrint()
	self.Answer("", true)
	self.Playback(audioFile, "1", "", true, 1)
	self.Conference("test", "", true)
	self.Hangup("NO_USER_RESPONSE", "", true)

}
func (self *OutboundManager) createHandlers() (handlers map[string][]func(*fsswitch.Event)) {
	hb := func(ev *fsswitch.Event) {
		self.OnHeartBeat(ev)
	}
	ap := func(ev *fsswitch.Event) {

		self.OnCEC(ev)
	}

	return map[string][]func(*fsswitch.Event){
		"HEARTBEAT":                []func(*fsswitch.Event){hb},
		"CHANNEL_EXECUTE_COMPLETE": []func(*fsswitch.Event){ap},
	}
}
func (self *OutboundManager) OnHeartBeat(ev *fsswitch.Event) {
	log.Println(" HB Correct!")
	ev, err := self.APICommand("sofia status")
	if err != nil {
		log.Printf("Originate Error:%s", err)
	}
	ev.PrettyPrint()
}
func (self *OutboundManager) OnCEC(ev *fsswitch.Event) {
	log.Printf("Application is %s", ev.GetHeader("Application", "None"))
}

func main() {
	fsswitch.OutboundServer(":9001", NewOutboundManager)
}
