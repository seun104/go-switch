package main

import (
	"errors"
	"fmt"
	"github.com/temlioinc/go-switch"
	"log"
)

type InboundManager struct {
	fsaddress  string
	fspassword string
	reconnects int
	*fsswitch.InboundSocket
}

func NewManager(fsaddress, fspassword string, reconnects int) *InboundManager {
	log.Println("NewManager")
	return &InboundManager{fsaddress: fsaddress, fspassword: fspassword, reconnects: reconnects}
}
func (self *InboundManager) Start() (err error) {
	self.InboundSocket, err = fsswitch.NewInboundSocket(self.fsaddress, self.fspassword, self.reconnects, false, self.createHandlers())
	//self.InboundFS, err
	if err != nil {
		log.Println("an error occured")
	}
	self.Originate(dest, dialplan)
	self.Start()
	return errors.New("<Manager> - Stopped reading events")
	//return nil
}
func (self *InboundManager) createHandlers() (handlers map[string][]func(*fsswitch.Event)) {
	hb := func(ev *fsswitch.Event) {
		self.OnHeartBeat(ev)
	}
	ap := func(ev *fsswitch.Event) {

		self.OnApi(ev)
	}

	return map[string][]func(*fsswitch.Event){
		"HEARTBEAT": []func(*fsswitch.Event){hb},
		"API":       []func(*fsswitch.Event){ap},
	}
}
func (self *InboundManager) OnHeartBeat(ev *fsswitch.Event) {

	log.Println(" HB Correct!")
	evnt, err := self.APICommand("sofia status")
	log.Println("API Command response:%s", evnt.String())
	evnt.String()
	if err != nil {
		log.Printf("Originate Error:%s", err)
	}
}
func (self *InboundManager) OnApi(ev *fsswitch.Event) {

	log.Println("API Correct!")
}
func (self *InboundManager) Originate(dest, dialplan string) error {

	log.Printf("Sending Command")
	evnt, err := self.APICommand(fmt.Sprintf("originate %s %s", dest, dialplan))
	log.Println("API Command response:%s", evnt.String())
	evnt.String()
	if err != nil {
		log.Printf("Originate Error:%s", err)
	}
	return nil
}

const dest = "{origination_caller_id_number=+2348038207883,ignore_early_media=true}sofia/gateway/idt2/999002348038207883"
const dialplan = "&conference(test)"

func main() {
	//var err error
	manager := NewManager("127.0.0.1:8021", "CluCon", 100)
	manager.Start()

}
