package main

import (
    "flag"
    "fmt"
    "log"
    "os"
    "runtime"
    "runtime/pprof"
    "sync"
    "sync/atomic"
    "time"

    "github.com/packing/clove/codecs"
    "github.com/packing/clove/env"
    "github.com/packing/clove/messages"
    "github.com/packing/clove/nnet"
    "github.com/packing/clove/packets"
    "github.com/packing/clove/utils"
)

var (
    help    bool
    version bool

    pprofFile string
    addr      string
    port      int
    thread    int
    count     int

    rv int64
    sv int
    av int64

    dv int64 = 0

    st time.Time

    logLevel = utils.LogLevelError

    //tcp *net.TCPClient = nil

    sendch = new(sync.Map)
    sendc = new(sync.Map)
)

func usage() {
    fmt.Fprint(os.Stderr, `client

Usage: client [-hv] [-a addr] [-p port] [-t hread] [-c count] [-f pprof file]

Options:
`)
    flag.PrintDefaults()
}

func OnS2CDataDecoded(controller nnet.Controller, addr string, data codecs.IMData) error {
    //utils.LogInfo("OnS2CDataDecoded", data)
    msg, err := messages.MessageFromData(controller, addr, data)
    if err != nil {
        utils.LogInfo("收到错误的消息封包", err)
        return err
    }

    if msg.GetType() == messages.ProtocolTypeHeart {
        body := msg.GetBody()
        reader := codecs.CreateMapReader(body)
        tv := reader.IntValueOf(messages.ProtocolKeyValue, 0)
        atomic.AddInt64(&dv, time.Now().UnixNano() - tv)
        //utils.LogInfo("收到心跳回应, 基准时间 -> %d + %d", tv, dv)
        atomic.AddInt64(&rv, 1)
    }

    fmt.Printf("发送 / 回应: %d / %d, 总数 %d, 平均 %.4f / 毫秒\r", sv, rv, thread*count, float64(dv)/float64(rv)/float64(time.Millisecond))

    ic, ok := sendc.Load(controller.GetSessionID())
    var c = 0
    if ok {
        c, _ = ic.(int)
    }
    sendc.Store(controller.GetSessionID(), c + 1)

    if c + 1 >= count {
        ich, ok := sendch.Load(controller.GetSessionID())
        if ok {
            ch, ok := ich.(chan int)
            if ok {
                close(ch)
            }
        }
        //utils.LogInfo("close send channel. %d                                            ", controller.GetSessionID())
        controller.Close()
        sendch.Delete(controller.GetSessionID())
    } else {
        go func() {
            ich, ok := sendch.Load(controller.GetSessionID())
            if ok {
                ch, ok := ich.(chan int)
                if ok {
                    ch <- 1
                }
            }
        }()
    }

    return nil
}

func sayHello(tcp *nnet.TCPClient) error {

    ich, ok := sendch.Load(tcp.GetSessionID())
    if ok {
        ch, ok := ich.(chan int)
        if ok {
            _, ok := <- ch
            if !ok {
                //utils.LogInfo("send channel %d closed                              ", tcp.GetSessionID())
                tcp.Close()
                return nil
            }
        } else {
            tcp.Close()
            return nil
        }
    } else {
        tcp.Close()
        return nil
    }

    defer func() {
        utils.LogPanic(recover())
    }()

    msg := messages.CreateC2SMessage(messages.ProtocolTypeHeart)
    msg.SetTag(messages.ProtocolTagSlave)
    req := codecs.IMMap{}
    req[messages.ProtocolKeyValue] = time.Now().UnixNano()
    req[messages.ProtocolKeySerial] = 1130.117
    msg.SetBody(req)
    pck, err := messages.DataFromMessage(msg)
    if err == nil {
        tcp.Send(pck)
        sv += 1
    } else {

    }

    if rv > 0 {
        fmt.Printf("发送 / 回应: %d / %d, 总数 %d, 平均 %.4f / 毫秒\r", sv, rv, thread*count, float64(dv)/float64(rv)/float64(time.Millisecond))
    }
    return err
}

func main() {
    runtime.GOMAXPROCS(1)
    rv = 0
    sv = 0

    flag.BoolVar(&help, "h", false, "this help")
    flag.BoolVar(&version, "v", false, "print version")
    flag.StringVar(&addr, "a", "127.0.0.1", "dest addr")
    flag.IntVar(&port, "p", 10086, "dest port")
    flag.IntVar(&thread, "t", 1, "thread count")
    flag.IntVar(&count, "c", 1, "count of signle-thread")
    flag.StringVar(&pprofFile, "f", "", "pprof file")
    flag.Usage = usage

    flag.Parse()
    if help {
        flag.Usage()
        return
    }
    if version {
        fmt.Println("client version 1.0")
        return
    }

    var pproff *os.File = nil
    if pprofFile != "" {
        pf, err := os.OpenFile(pprofFile, os.O_RDWR|os.O_CREATE, 0644)
        if err != nil {
            log.Fatal(err)
        }
        pproff = pf
        pprof.StartCPUProfile(pproff)
    }

    defer func() {
        if pproff != nil {
            pprof.StopCPUProfile()
            pproff.Close()
        }

        utils.LogInfo(">>> 进程已退出")
    }()

    utils.LogInit(logLevel, "")
    //注册解码器
    env.RegisterCodec(codecs.CodecIMv2)

    //注册通信协议
    env.RegisterPacketFormat(packets.PacketFormatNB)

    nnet.SetSendBufSize(10240)
    nnet.SetRecvBufSize(10240)

    var tcps = make(map[nnet.SessionID]*nnet.TCPClient)

    for j := 0; j < thread; j ++ {
        tcp := nnet.CreateTCPClient(packets.PacketFormatNB, codecs.CodecIMv2)
        tcp.OnDataDecoded = OnS2CDataDecoded
        err := tcp.Connect(addr, port)
        if err != nil {
            utils.LogError("!!!无法连接到指定tcp地址 %s:%d", addr, port, err)
            tcp.Close()
            return
        }
        utils.LogInfo("连接 %d 就绪", tcp.GetSessionID())
        tcps[tcp.GetSessionID()] = tcp
        ch := make(chan int)
        sendch.Store(tcp.GetSessionID(), ch)
        go func() {
            ch <- 1
        }()
        //time.Sleep(1 * time.Millisecond)
    }

    st = time.Now()
    for _, tcp := range tcps {
        go func(tcp *nnet.TCPClient) {
            utils.LogInfo("连接 %d 开始", tcp.GetSessionID())
            for i := 0; i < count; i++ {
                sayHello(tcp)
            }
            //close(sendch[tcp.GetSessionID()])
            //delete(sendch, tcp.GetSessionID())
        }(tcp)
    }

    //utils.LogInfo(">>> 当前协程数量 > %d", runtime.NumGoroutine())
    //开启调度，主线程停留在此等候信号
    env.Schedule()

    fmt.Print("\r\n")

}
