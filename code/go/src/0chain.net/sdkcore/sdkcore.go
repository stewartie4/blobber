package sdkcore

import (
	"fmt"
	"bytes"
	"bufio"
	"github.com/klauspost/reedsolomon"
)

const (
	DATA_SHARDS_DEFAULT   = 10
	PARITY_SHARDS_DEFAULT = 3
)

type EncoderInterface interface {
	Encode(inputData []byte) (int, error)
	GetEncodedDataPart(shardNum int) ([]byte)
	SetDataToDecodePart(shardNum int, dataPart []byte) (error)
	Decode() ([]byte, error)
}

type Encoder struct {
	DataShards   	int
	ParityShards 	int
	encoder      	reedsolomon.Encoder
	encodedData		[][]byte
	dataToDecode    [][]byte
}

func New(numDataShards, numParityShards int) *Encoder {
	enc := &Encoder{}
	var err error
	enc.encoder, err = reedsolomon.New(numDataShards, numParityShards)
	if err != nil {
		fmt.Println("Creating encoder failed: ",err.Error())
		return nil
	}
	enc.DataShards = numDataShards
	enc.ParityShards = numParityShards
	enc.dataToDecode = make([][]byte, numDataShards+numParityShards)
	return enc
}

func (enc *Encoder) Encode(inputData []byte) (int, error) {
	var err error
	enc.encodedData, err = enc.encoder.Split(inputData)
	if err != nil {
		fmt.Println("Split failed", err.Error())
		return 0, err
	}
	err = enc.encoder.Encode(enc.encodedData)
	if err != nil {
		fmt.Println("Encode failed", err.Error())
		return 0, err
	}
	return enc.DataShards+enc.ParityShards , nil
}

func (enc *Encoder) GetEncodedDataPart(shardNum int) ([]byte) {
	if (shardNum < enc.DataShards + enc.ParityShards) {
		return enc.encodedData[shardNum]
	}
	return []byte{}
}

func (enc *Encoder) SetDataToDecodePart(shardNum int, dataPart []byte) (error) {
	if (shardNum < enc.DataShards + enc.ParityShards) {
		enc.dataToDecode[shardNum] = dataPart
		return nil
	}
	return fmt.Errorf("Invalid %d shard", shardNum)
}

func (enc *Encoder) Decode() ([]byte, error) {
	_, err := enc.encoder.Verify(enc.dataToDecode)
	if err != nil {
		fmt.Println("Verification failed. Reconstructing data")
		err = enc.encoder.Reconstruct(enc.dataToDecode)
		if err != nil {
			fmt.Println("Reconstruct failed -", err)
			return []byte{}, err
		}
		_, err = enc.encoder.Verify(enc.dataToDecode)
		if err != nil {
			fmt.Println("Verification failed after reconstruction, data likely corrupted.", err.Error())
			return []byte{}, err
		}
	}
	var bytesBuf bytes.Buffer
	bufWriter := bufio.NewWriter(&bytesBuf)
	bytesPerShard := len(enc.dataToDecode[0])
	bufWriter = bufio.NewWriterSize(bufWriter, (bytesPerShard * enc.DataShards))
	err = enc.encoder.Join(bufWriter, enc.dataToDecode, (bytesPerShard * enc.DataShards))
	if err != nil {
		fmt.Println("join failed", err.Error())
		return []byte{}, err
	}
	bufWriter.Flush()
	outBuf := bytesBuf.Bytes()
	return outBuf, nil
}
