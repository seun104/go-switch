/*
go-switch is released under the MIT License <http://www.opensource.org/licenses/mit-license.php
Copyright (C) Temlio Inc. All Rights Reserved.

Provides FreeSWITCH socket communication.

*/
package fsswitch

import (
	"fmt"
	_ "log"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type EventHeader map[string]string

// Event represents a FreeSWITCH event.
type Event struct {
	Header map[string]string // Event headers, key:val
	Body   string            // Raw body, available in some events
}

func (self *Event) String() string {
	if self.Body == "" {
		return fmt.Sprintf("%s", self.Header)
	} else {
		return fmt.Sprintf("%s body=%s", self.Header, self.Body)
	}
}

// PrettyPrint prints Event headers and body to the standard output.
func (self *Event) PrettyPrint() {
	var keys []string
	for k := range self.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s: %#v\n", k, self.Header[k])
	}
	if self.Body != "" {
		fmt.Printf("BODY: %#v\n", self.Body)
	}
}

// Get returns an Event value, or "" if the key doesn't exist.
func (self *Event) GetHeader(key, defaultValue string) string {
	if hdr := self.Header[key]; hdr != "" {
		//log.Printf("Header:%s, Val:%s", key, hdr)
		return hdr
	}
	return defaultValue
}

// GetInt returns an Event value converted to int, or an error if conversion
// is not possible.
func (self *Event) GetInt(key string) (int, error) {
	n, err := strconv.Atoi(self.Header[key])
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (self *Event) GetContentLength() int {
	//Gets Content-Length header as integer.  Returns 0 If length not found.
	conLen := self.GetHeader("Content-Length", "0")
	n, err := strconv.Atoi(conLen)
	if err != nil {
		return 0
	}
	return n
}

func (self *Event) GetReplyText() string {
	if reply := self.GetHeader("Reply-Text", ""); reply != "" {
		return reply
	}
	return ""
}
func (self *Event) IsReplyTextSuccess() bool {
	/*
	   Returns True if ReplyText header contains OK.
	   Returns False otherwise.
	*/
	if reply := self.GetReplyText(); strings.Contains(reply, "OK") {
		return true
	}
	return false
}
func (self *Event) GetContentType() string {
	/*
	   Gets Content-Type header as string.
	   Returns None if header not found.
	*/

	return self.GetHeader("Content-Type", "")
}

// FS event header values are urlencoded. Use this to decode them. On error, use original value
func urlDecode(hdrVal string) string {
	if valUnescaped, errUnescaping := url.QueryUnescape(hdrVal); errUnescaping == nil {
		hdrVal = valUnescaped
	}
	return hdrVal
}
func EventStrToMap(fsevstr string) map[string]string {
	fsevent := make(map[string]string)
	for _, strLn := range strings.Split(fsevstr, "\n") {
		if hdrVal := strings.SplitN(strLn, ": ", 2); len(hdrVal) == 2 {
			fsevent[hdrVal[0]] = urlDecode(strings.TrimSpace(strings.TrimRight(hdrVal[1], "\n")))
		}
	}
	return fsevent
}
