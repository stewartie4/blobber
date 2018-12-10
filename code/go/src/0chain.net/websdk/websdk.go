package main

import (
	"fmt"
	"reflect"
	"syscall/js"
	"unsafe"
	"0chain.net/sdkcore"
)

type ZCNStreamEncoder struct {
	encoder			*sdkcore.Encoder
	setupCb 		js.Callback
	encodeCb		js.Callback
	decodeCb		js.Callback
	unloadCh 		chan struct {}
	unloadCb		js.Callback
}

var WebSDK ZCNStreamEncoder

func init() {
	WebSDK.setupCb			=	js.NewCallback(ZCNWebSdkSetup)
	WebSDK.encodeCb			=	js.NewCallback(ZCNWebSdkEncode)
	WebSDK.decodeCb			= 	js.NewCallback(ZCNWebSdkDecode)
	WebSDK.unloadCh			= 	make(chan struct{})
	WebSDK.unloadCb			= 	js.NewCallback(ZCNWebSdkUnload)
}

// args[0] : Number of data shards
// args[1] : Number of parity shards
func ZCNWebSdkSetup(args []js.Value) {
	if (len(args) < 2) {
		return
	}
	dataShards 		:= args[0].Int()
	parityShards	:= args[1].Int()
	WebSDK.encoder = sdkcore.New(dataShards, parityShards)
}

// args[0] : Uint8Array JS buffer
// args[1] : Callback function for encoded data
func ZCNWebSdkEncode(args []js.Value) {
	if (len(args) < 2) {
		return
	}
	inputJsData := js.ValueOf(args[0])
	// Copy js data to go buffer
	inputData := make([]byte, inputJsData.Length())
	for i := 0; i < inputJsData.Length(); i++ {
		inputData[i] = byte(inputJsData.Index(i).Int())
	}
	
	numShards, err := WebSDK.encoder.Encode(inputData)
	if err != nil {
		fmt.Println("ZCNWebSdkEncode(): ", err.Error())
		return
	}

	for i := 0; i < numShards; i++ {
		data := WebSDK.encoder.GetEncodedDataPart(i)
		hdr := (*reflect.SliceHeader)(unsafe.Pointer(&data))
		ptr := uintptr(unsafe.Pointer(hdr.Data))
		js.Global().Call(js.ValueOf(args[1]).String(), i, len(data), ptr)
	}
}

// args[0]..[x] = Number of shards (data + parity)
// args[x+1] = Callback function of joined buffer
func ZCNWebSdkDecode(args []js.Value) {
	if (len(args) < 2) {
		return
	}
	inputJsData 	:= js.ValueOf(args[0])
	numshards	  	:= inputJsData.Length()
	var inputData [][]byte
	var bytesPerShard int
	inputData 		= make([][]byte, numshards)
	for shards := 0; shards < numshards; shards++ {
		jsShard  		:= js.ValueOf(inputJsData).Index(shards)
		bytesPerShard	= js.ValueOf(jsShard).Length()
		// Copy js data to go buffer
		inputData[shards] = make([]byte, bytesPerShard)
		for i := 0; i < bytesPerShard; i++ {
			inputData[shards][i] = byte(jsShard.Index(i).Int())
		}
		err := WebSDK.encoder.SetDataToDecodeShard(shards, inputData[shards])
		if err != nil  {
			fmt.Println(err)
			return
		}
	}
	outBuf, err := WebSDK.encoder.Decode()
	if err != nil {
		js.Global().Call(js.ValueOf(args[1]).String(), 0, 0)
		return
	}
	// fmt.Println(bytesBuf.Len(), outBuf)
	hdr := (*reflect.SliceHeader)(unsafe.Pointer(&outBuf))
	ptr := uintptr(unsafe.Pointer(hdr.Data))
	js.Global().Call(js.ValueOf(args[1]).String(), len(outBuf), ptr)
}

// No argument
func ZCNWebSdkUnload(args []js.Value) {
	WebSDK.unloadCh <- struct {} {}
}


func exportFunctions() {
	js.Global().Set("ZCNWebSdkSetup", WebSDK.setupCb)
	js.Global().Set("ZCNWebSdkEncode", WebSDK.encodeCb)
	js.Global().Set("ZCNWebSdkDecode", WebSDK.decodeCb)
	js.Global().Set("ZCNWebSdkUnload", WebSDK.unloadCb)
}

func releaseFunctions() {
	WebSDK.setupCb.Release()
	WebSDK.encodeCb.Release()
	WebSDK.decodeCb.Release()
	WebSDK.unloadCb.Release()
}


func main() {
	exportFunctions()
	fmt.Println("0Chain WebSDK WASM Initialized!!")
	// Wait for beforeUnload event to cleanup resource
	<-WebSDK.unloadCh
	releaseFunctions()
	fmt.Println("0Chain WebSDK WASM Uninitialized!!")
}
