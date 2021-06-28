package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"wsvms/websocket"

	"github.com/jinzhu/configor"
)

type baseConfig struct {
	APPName       string `default:"app name"`
	IntelliServer string `default:"192.168.2.50:7681"`
	Id            string `default:"intellivi"`
	Password      string `default:"pass0001"`
	Imgpath       string `default:"./"`
	MrsServer     string `default:"192.168.11.100:9201"`
}

var config = baseConfig{}

const (
	evType0 = iota
	evType1
	evType2
	evType3
	evType4
	evType5
	evType6
	evType7
	evType8
	evType9
	evType10
	evType11
	evType12
	evType13
	evType14
	evType15
	evType16
	evType17
	evType18
	evType19
	evType20
	evType21
	evType22
	evType23
	evType24
	evType25
	evType26
	evType27
	evType28
	evType29
	evType30
	evType31
	evType32
)

var evTypeString = map[int]string{
	0:  "배회",
	1:  "경로 통과",
	2:  "방향성 이동",
	3:  "진입",
	4:  "진출",
	5:  "멈춤",
	6:  "버려짐",
	7:  "경계선 통과",
	8:  "연기",
	9:  "불꽃",
	10: "쓰러짐",
	11: "군집",
	12: "폭력",
	13: "멀티 경계선 통과",
	14: "차량 사고",
	15: "차량 멈춤",
	16: "교통 정체",
	17: "색상 변화",
	18: "차량 주차",
	19: "제거됨",
	20: "위험수위",
	21: "영역색상",
	22: "체류",
	23: "체류-초과인원",
	24: "체류-시간초과",
	25: "체류-단독인원체류",
	26: "안전모 미착용",
	27: "홀로 남겨짐",
	28: "접근",
	29: "따로 떨어짐",
	30: "행동인식",
}

func main() {
	// syscall interupt시 goroutine 정리용
	ctx, cancel := context.WithCancel(context.Background())

	// ctrl-c 혹은 kill 시 메시지 받음.
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	// config file
	error := configor.New(&configor.Config{
		AutoReload:         false,
		AutoReloadInterval: 15 * time.Second,
		AutoReloadCallback: func(config interface{}) {
			fmt.Printf("====================================\n%v changed \n", config)
			fmt.Printf("====================================\n")
			// ss := config.(*baseConfig)
		},
		Verbose: true,
	}).Load(&config, "./config.yaml")
	if error != nil {
		log.Fatal(error)
		os.Exit(1)
	}

	go process(ctx)

	// 메인 프로세스는 여기서 대기하다가 sysint interupt 메시지 받으면
	// goroutine 정리하고 종료.
	<-interrupt
	cancel()
	fmt.Println("wait moment")
	// 네트워크 물고 있는 goroutine 정리까지 2초 기다려줌.
	time.Sleep(2 * time.Second)
	fmt.Println("bye bye os!")
}

func process(ctx context.Context) {

	var (
		apiKey   string
		loginerr error
	)

	// 무한 반복. 단 sys interupt 시만 빠져 나감.
	for {
		// 빠져나갈때 socket goroutine 정리용
		ctxp, cancelp := context.WithCancel(ctx)
		for {
			// 로그인하여 api 키를 가져옴.
			apiKey, loginerr = loginToVideo()
			if loginerr == nil {
				break
			}

			log.Println("login error")
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			select {
			case <-ctx.Done():
				log.Println("login process func interrupt")

			case <-ticker.C:
				continue
			}
		}

		keepErrChan := make(chan struct{})
		go keep(ctx, apiKey, keepErrChan)

		wsurl := "ws://" + config.IntelliServer + "/vaMetadata?api-key=" + apiKey + "&evtAlmMeta=fullImg"
		socket := websocket.New(wsurl)
		socket.ConnectionOptions = websocket.ConnectionOptions{
			UseSSL:         false,
			UseCompression: false,
			Subprotocols:   []string{"va-metadata"},
		}

		socket.EnableLogging()

		var infileName string

		socket.OnConnectError = func(err error, socket websocket.WebSocket) {
			log.Fatal("Recieved connect error ", err)
		}

		socket.OnConnected = func(socket websocket.WebSocket) {
			log.Println("Connected to server")
		}

		socket.OnTextMessage = func(message []byte, socket websocket.WebSocket) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("save file error", r)
				}
			}()

			log.Println("Recieved message  " + string(message))
			var data map[string]interface{}
			jserr := json.NewDecoder(bytes.NewReader(message)).Decode(&data)
			if jserr != nil {
				log.Println("read:", jserr)
				return
			}

			evTT := data["evtAlm"].(map[string]interface{})["type"].(float64)
			if val, ok := evTypeString[int(evTT)]; ok {
				log.Println(val, "+++++++++++++++++++++++++++++++++++++++")
			}
			infileName = strconv.FormatFloat(data["evtAlm"].(map[string]interface{})["id"].(float64), 'f', -1, 64)
			infileName += "-" + data["evtAlm"].(map[string]interface{})["tm"].(string)
			log.Println("infileName", infileName)
			// send message to mrs
			go sendMessage()
		}

		socket.OnBinaryMessage = func(message []byte, socket websocket.WebSocket) {
			saveFrames(message, infileName)
		}

		socket.OnPingReceived = func(data string, socket websocket.WebSocket) {
			log.Println("Recieved ping " + data)
		}

		socket.OnDisconnected = func(err error, socket websocket.WebSocket) {
			log.Println("Disconnected from server ")
			return
		}

		go socket.Connect(ctxp)

		select {
		case <-ctx.Done():
			log.Println("process func interrupt")
			socket.Close()
			cancelp()
			return
		case <-keepErrChan:
			log.Println("keep alive error ")
			socket.Close()
			cancelp()
			break
		}

		fmt.Println("process restart")
	}
}

func loginToVideo() (string, error) {
	authurl := "http://" + config.IntelliServer + "/users/login"
	fmt.Println("URL:>", authurl)

	var jsonStr = []byte(`{"id": "` + config.Id + `", "pw": "` + config.Password + `" }`)
	req, err := http.NewRequest("POST", authurl, bytes.NewBuffer(jsonStr))

	if err != nil {
		fmt.Println("err on connect")
		return "", errors.New("login server connect fail")
	}

	req.Header.Set("Content-Type", "application/json")

	authclient := &http.Client{}
	resp, err := authclient.Do(req)
	if err != nil {
		fmt.Println("err on request")
		return "", errors.New("login server request fail")
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	jsonerr := json.NewDecoder(resp.Body).Decode(&data)
	if jsonerr != nil {
		fmt.Println("err on response json decode")
		return "", errors.New("login server response error")
	}

	var apikey string
	if resapi, found := data["api-key"]; found {
		apikey = resapi.(string)
	} else {
		fmt.Println("err on apikey")
		return "", errors.New("apikey not found in login server response")
	}

	log.Println("apikey =============>   ", apikey)
	return apikey, nil
}

func keep(ctx context.Context, apiKey string, errchan chan struct{}) {
	authurl := "http://" + config.IntelliServer + "/keepalive"
	var jsonStr = []byte(`{ }`)
	req, err := http.NewRequest("POST", authurl, bytes.NewBuffer(jsonStr))
	if err != nil {
		fmt.Println("err on keep connect")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)
	ticker := time.NewTicker(time.Minute * 14)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("keep process canceled")
			return
		case <-ticker.C:
			authclient := &http.Client{}
			resp, err := authclient.Do(req)
			if err != nil {
				log.Println(err)
				errchan <- struct{}{}
				return
			}
			defer resp.Body.Close()
			fmt.Println("keep response Status:", resp.Status)
			if resp.StatusCode > 300 {
				log.Println("keep process : api-key is invalid ")
				errchan <- struct{}{}
				return
			}
		}
	}
}

func saveFrames(imgByte []byte, tm string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("save file error", r)
		}
	}()

	fmt.Println("saveframes", tm)
	img, _, _ := image.Decode(bytes.NewReader(imgByte))
	out, err := os.Create(config.Imgpath + tm + ".jpeg")
	if err != nil {
		panic(err)
	}

	err = jpeg.Encode(out, img, nil)
	if err != nil {
		panic(err)
	}
}
