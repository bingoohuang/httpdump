package main

import (
	"context"
	"fmt"
	"time"

	"github.com/bingoohuang/gg/pkg/ctx"
	"github.com/bingoohuang/gg/pkg/flagparse"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

const (
	LevelL0     = "l0"
	LevelUrl    = "url"
	LevelHeader = "header"
	LevelAll    = "all"
)

// Option Command line options.
type Option struct {
	Level    string        `val:"header" usage:"Output level, options are: l1(first line) | url(only url) | header(http headers) | all(headers, and textuary http body)"`
	Input    string        `flag:"i" val:"any" usage:"Interface name or pcap file. If not set, If is any, capture all interface traffics"`
	Ip       string        `usage:"Filter by ip, if either source or target ip is matched, the packet will be processed"`
	Port     uint          `usage:"Filter by port, if either source or target port is matched, the packet will be processed."`
	Chan     uint          `val:"10240" usage:"Channel size to buffer tcp packets."`
	OutChan  uint          `val:"40960" usage:"Output channel size to buffer tcp packets."`
	Host     string        `usage:"Filter by request host, using wildcard match(*, ?)"`
	Uri      string        `usage:"Filter by request url path, using wildcard match(*, ?)"`
	Method   string        `usage:"Filter by request method, multiple by comma"`
	Resp     bool          `usage:"Print response or not"`
	Status   Status        `usage:"Filter by response status code. Can use range. eg: 200, 200-300 or 200:300-400"`
	Force    bool          `usage:"Force print unknown content-type http body even if it seems not to be text content"`
	Curl     bool          `usage:"Output an equivalent curl command for each http request"`
	Human    bool          `usage:"Output human readable"`
	DumpBody bool          `usage:"Dump http request/response body to file"`
	Fast     bool          `usage:"Fast mode, process request and response separately"`
	Output   string        `usage:"Write result to file [output] instead of stdout"`
	Idle     time.Duration `val:"4m" usage:"Idle time to remove connection if no package received"`
}

func main() {
	option := &Option{}
	flagparse.Parse(option)
	if err := option.run(); err != nil {
		panic(err)
	}
}

func (o *Option) run() error {
	if o.Port > 65536 {
		return fmt.Errorf("ignored invalid port %v", o.Port)
	}

	packets, err := createPacketsChan(o.Input, o.Host, o.Ip, o.Port)
	if err != nil {
		return err
	}

	printer := newPrinter(o.Output, o.OutChan)
	handler := o.createConnectionHandler(printer)
	assembler := newTCPAssembler(handler)
	assembler.human = o.Human
	assembler.chanSize = o.Chan
	assembler.filterIP = o.Ip
	assembler.filterPort = uint16(o.Port)

	c := ctx.RegisterSignals(nil)
	loop(c, packets, assembler, o.Idle)

	assembler.finishAll()
	printer.finish()
	return nil
}

type Sender interface {
	Send(msg string)
}

func (o *Option) createConnectionHandler(sender Sender) ConnectionHandler {
	if o.Fast {
		return &FastConnectionHandler{option: o, sender: sender}
	}

	return &HTTPConnectionHandler{option: o, sender: sender}
}

func loop(ctx context.Context, packets chan gopacket.Packet, assembler *TCPAssembler, idle time.Duration) {
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()

	for {
		select {
		case p := <-packets:
			if p == nil { // A nil p indicates the end of a pcap file.
				return
			}

			// only assembly tcp/ip packets
			n, t := p.NetworkLayer(), p.TransportLayer()
			if n == nil || t == nil || t.LayerType() != layers.LayerTypeTCP {
				continue
			}

			assembler.assemble(n.NetworkFlow(), t.(*layers.TCP), p.Metadata().Timestamp)
		case <-ticker.C:
			// flush connections that haven't been activity in the idle time
			assembler.flushOlderThan(time.Now().Add(-idle))
		case <-ctx.Done():
			return
		}
	}
}

type Status IntSet

func (i *Status) String() string { return "" }

func (i *Status) Set(value string) error {
	set, err := ParseIntSet(value)
	if err != nil {
		return err
	}
	*i = Status(*set)
	return nil
}
