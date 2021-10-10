package main

/*
 * websocket/pty proxy server:
 * This program wires a websocket to a pty master.
 *
 * Usage:
 * go build -o ws-pty-proxy server.go
 * ./websocket-terminal -cmd /bin/bash -addr :9000 -static $HOME/src/websocket-terminal
 * ./websocket-terminal -cmd /bin/bash -- -i
 */

import (
	"encoding/base64"
	"flag"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
)

var addrFlag, cmdFlag, staticFlag string

var upgrader = websocket.Upgrader{
	// 允许跨域
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type wsPty struct {
	Cmd *exec.Cmd // os.exec 执行命令
	Pty *os.File  // 开启一个 pty
}

func (wp *wsPty) Start() {
	var err error
	args := flag.Args()
	wp.Cmd = exec.Command(cmdFlag, args...)
	wp.Pty, err = pty.Start(wp.Cmd)
	if err != nil {
		log.Fatalf("Failed to start command: %s\n", err)
	}
}

func (wp *wsPty) Stop() {
	// TODO 是否可以用 defer
	wp.Pty.Close()
	wp.Cmd.Wait()
}

// http 请求处理
func ptyHandler(w http.ResponseWriter, r *http.Request) {
	// 协议升级
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatalf("WebSocket upgrade failed: %s\n", err)
	}
	defer conn.Close()

	wp := wsPty{}
	// TODO 检测错误并返回 500
	wp.Start()

	// 将 pty 中的内容传输给 ws
	go func() {
		buf := make([]byte, 128)
		// TODO 优雅地关闭进程和 ws 连接
		for {
			// 从 pty 读取
			n, err := wp.Pty.Read(buf)
			if err != nil {
				log.Printf("Failed to read from pty master: %s", err)
				return
			}

			// TODO 改成传输 JSON 字符串
			out := make([]byte, base64.StdEncoding.EncodedLen(n))
			base64.StdEncoding.Encode(out, buf[0:n])

			// 向 ws 管道中写入
			err = conn.WriteMessage(websocket.TextMessage, out)

			if err != nil {
				log.Printf("Failed to send %d bytes on websocket: %s", n, err)
				return
			}
		}
	}()

	// 从 ws 中读取并拷贝到 pty 中
	// 读取到的应该是 base64 编码的 text 类型字符串
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			if err != io.EOF {
				log.Printf("conn.ReadMessage failed: %s\n", err)
				return
			}
		}

		switch messageType {
		case websocket.TextMessage:
			// TODO 若改为 JSON 数据，此处解码 base64 还需判断数据类型
			buf := make([]byte, base64.StdEncoding.DecodedLen(len(payload)))
			_, err := base64.StdEncoding.Decode(buf, payload)
			if err != nil {
				log.Printf("base64 decoding of payload failed: %s\n", err)
			}
			wp.Pty.Write(buf)
		default:
			log.Printf("Invalid message type %d\n", messageType)
			return
		}
	}

	wp.Stop()
}

func init() {
	cwd, _ := os.Getwd() // 获取工作路径，用于 host 静态文件
	flag.StringVar(&addrFlag, "addr", ":9000", "IP:PORT or :PORT address to listen on")
	flag.StringVar(&cmdFlag, "cmd", "/bin/bash", "command to execute on slave side of the pty")
	flag.StringVar(&staticFlag, "static", cwd, "path to static content")
}

func main() {
	flag.Parse()

	http.HandleFunc("/pty", ptyHandler)

	// host 静态文件：html, js, css, etc.
	http.Handle("/", http.FileServer(http.Dir(staticFlag)))

	err := http.ListenAndServe(addrFlag, nil)
	if err != nil {
		log.Fatalf("net.http could not listen on address '%s': %s\n", addrFlag, err)
	}
}
