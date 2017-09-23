// Copyright 2017 The master-pfa-info Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package imgsrv

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"go-hep.org/x/hep/hplot"
	"golang.org/x/net/websocket"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

// Paint draws the pixels data to a web page.
func Paint(title string, pixels [][]uint8) {
	<-srv.wait
	srv.datac <- renderImage(title, pixels)
	<-srv.quit
}

// Print prints the image to a file.
func Print(fname string) {
	img := <-srv.img
	f, err := os.Create(fname)
	if err != nil {
		log.Fatalf("error creating file: %v", err)
	}
	defer f.Close()

	err = png.Encode(f, img.img)
	if err != nil {
		log.Fatalf("error saving image to PNG: %v", err)
	}

	err = f.Close()
	if err != nil {
		log.Fatalf("error saving to file %q: %v", f.Name(), err)
	}
}

type wimage struct {
	Title string `json:"title"`
	Image string `json:"image"`
	img   image.Image
}

func renderImage(title string, pixels [][]uint8) wimage {
	dx := len(pixels[0])
	dy := len(pixels)
	img := image.NewNRGBA(image.Rect(0, 0, len(pixels[0]), len(pixels)))

	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		log.Fatalf("error encoding image to PNG: %v", err)
	}

	return wimage{
		img:   img,
		Image: base64.StdEncoding.EncodeToString(buf.Bytes()),
	}
}

var (
	srv *server
)

type server struct {
	datac chan wimage
	img   chan image.Image
	quit  chan int
	wait  chan int
	done  chan int
}

func newServer() *server {
	srv := &server{
		datac: make(chan wimage),
		img:   make(chan image.Image),
		quit:  make(chan int),
		wait:  make(chan int),
		done:  make(chan int),
	}

	go srv.serve()
	go srv.run()

	return srv
}

func (srv *server) run() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case wimg := <-srv.datac:
		case <-srv.done:
			log.Printf("final: n=%d", srv.n)
			srv.plots <- plot(srv.n, srv.in, srv.out)
			time.Sleep(1 * time.Second) // give the server some time to update
			srv.quit <- 1
			return
		}
	}
}

func renderImg(p *hplot.Plot) string {
	size := 20 * vg.Centimeter
	canvas := vgimg.PngCanvas{vgimg.New(size, size)}
	p.Draw(draw.New(canvas))
	out := new(bytes.Buffer)
	_, err := canvas.WriteTo(out)
	if err != nil {
		log.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

type wplot struct {
	Plot string `json:"plot"`
}

func (srv *server) serve() {
	port, err := getTCPPort()
	if err != nil {
		log.Fatal(err)
	}
	ip := getIP()
	log.Printf("listening on %s:%s", ip, port)

	http.HandleFunc("/", plotHandle)
	http.Handle("/data", websocket.Handler(dataHandler))
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("error running web-server: %v", err)
	}
}

func plotHandle(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, page)
	select {
	case srv.wait <- 1:
	default:
	}
}

func dataHandler(ws *websocket.Conn) {
	for data := range srv.plots {
		err := websocket.JSON.Send(ws, data)
		if err != nil {
			log.Printf("error sending data: %v\n", err)
		}
	}
}

const page = `
<html>
	<head>
		<title>Monte Carlo</title>
		<script type="text/javascript">
		var sock = null;
		var plot = "";

		function update() {
			var p = document.getElementById("plot");
			p.src = "data:image/png;base64,"+plot;
		};

		window.onload = function() {
			sock = new WebSocket("ws://"+location.host+"/data");

			sock.onmessage = function(event) {
				var data = JSON.parse(event.data);
				plot = data.plot;
				update();
			};
		};

		</script>
	</head>

	<body>
		<div id="content">
			<p style="text-align:center;">
				<img id="plot" src="" alt="Not Available"></img>
			</p>
		</div>
	</body>
</html>
`

func getTCPPort() (string, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "", err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", err
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port), nil
}

func getIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}
