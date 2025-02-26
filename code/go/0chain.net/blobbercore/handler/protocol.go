package handler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"0chain.net/blobbercore/config"
	. "0chain.net/core/logging"
	"0chain.net/core/node"
	"0chain.net/core/transaction"

	"github.com/0chain/gosdk/zcncore"
	"go.uber.org/zap"
)

const (
	KB = 1024      // kilobyte
	MB = 1024 * KB // megabyte
	GB = 1024 * MB // gigabyte
)

type WalletCallback struct {
	wg  *sync.WaitGroup
	err string
}

func (wb *WalletCallback) OnWalletCreateComplete(status int, wallet string, err string) {
	wb.err = err
	wb.wg.Done()
}

// size in gigabytes
func sizeInGB(size int64) float64 { //nolint:unused,deadcode // might be used later?
	return float64(size) / GB
}

type apiResp struct { //nolint:unused,deadcode // might be used later?
	ok   bool
	resp string
}

func (ar *apiResp) decode(val interface{}) (err error) { //nolint:unused,deadcode // might be used later?
	if err = ar.err(); err != nil {
		return
	}
	return json.Unmarshal([]byte(ar.resp), val)
}

func (ar *apiResp) err() error { //nolint:unused,deadcode // might be used later?
	if !ar.ok {
		return errors.New(ar.resp)
	}
	return nil
}

// ErrBlobberHasRegistered represents double registration check error, where the
// blobber has already registered and shouldn't be passed through the registration flow again.
// To prevent duplicate instances.
var ErrBlobberHasRegistered = errors.New("blobber has registered")

func RegisterBlobber(ctx context.Context) (string, error) {
	wcb := &WalletCallback{}
	wcb.wg = &sync.WaitGroup{}
	wcb.wg.Add(1)
	err := zcncore.RegisterToMiners(node.Self.GetWallet(), wcb)
	if err != nil {
		return "", err
	}

	time.Sleep(transaction.SLEEP_FOR_TXN_CONFIRMATION * time.Second)

	// initialize storage node (ie blobber)
	txn, err := transaction.NewTransactionEntity()
	if err != nil {
		return "", err
	}

	sn, err := getStorageNode()
	if err != nil {
		return "", err
	}

	// check storage node (ie blobber): is it already registered?
	sRegisteredNodes, _ := GetBlobbers()

	for _, sRegisteredNode := range sRegisteredNodes {
		if sn.ID == string(sRegisteredNode.ID) || sn.BaseURL == sRegisteredNode.BaseURL {
			return "", ErrBlobberHasRegistered
		}
	}

	snBytes, err := json.Marshal(sn)
	if err != nil {
		return "", err
	}

	Logger.Info("Adding blobber to the blockchain.")
	err = txn.ExecuteSmartContract(transaction.STORAGE_CONTRACT_ADDRESS,
		transaction.ADD_BLOBBER_SC_NAME, string(snBytes), 0)
	if err != nil {
		Logger.Info("Failed during registering blobber to the mining network",
			zap.String("err:", err.Error()))
		return "", err
	}

	return txn.Hash, nil
}

// ErrBlobberHasRemoved represents service health check error, where the
// blobber has removed (by owner, in case the blobber doesn't provide its
// service anymore). Thus the blobber shouldn't send the health check
// transactions.
var ErrBlobberHasRemoved = errors.New("blobber has removed")

func BlobberHealthCheck(ctx context.Context) (string, error) {
	if config.Configuration.Capacity == 0 {
		return "", ErrBlobberHasRemoved
	}
	txn, err := transaction.NewTransactionEntity()
	if err != nil {
		return "", err
	}
	Logger.Info("Blobber health check to the blockchain.")
	err = txn.ExecuteSmartContract(transaction.STORAGE_CONTRACT_ADDRESS,
		transaction.BLOBBER_HEALTH_CHECK, "", 0)
	if err != nil {
		Logger.Info("Failed during blobber health check to the mining network",
			zap.String("err:", err.Error()))
		return "", err
	}

	return txn.Hash, nil
}

func UpdateBlobberSettings(ctx context.Context) (string, error) {
	txn, err := transaction.NewTransactionEntity()
	if err != nil {
		return "", err
	}

	sn, err := getStorageNode()
	if err != nil {
		return "", err
	}

	snBytes, err := json.Marshal(sn)
	if err != nil {
		return "", err
	}

	Logger.Info("Updating settings to the blockchain.")
	err = txn.ExecuteSmartContract(transaction.STORAGE_CONTRACT_ADDRESS,
		transaction.UPDATE_BLOBBER_SETTINGS, string(snBytes), 0)
	if err != nil {
		Logger.Info("Failed during updating settings to the mining network",
			zap.String("err:", err.Error()))
		return "", err
	}

	return txn.Hash, nil
}

func getStorageNode() (*transaction.StorageNode, error) {
	var err error
	sn := &transaction.StorageNode{}
	sn.ID = node.Self.ID
	sn.BaseURL = node.Self.GetURLBase()
	sn.Geolocation = transaction.StorageNodeGeolocation(config.Geolocation())
	sn.Capacity = config.Configuration.Capacity
	readPrice := config.Configuration.ReadPrice
	writePrice := config.Configuration.WritePrice
	if config.Configuration.PriceInUSD {
		readPrice, err = zcncore.ConvertUSDToToken(readPrice)
		if err != nil {
			return nil, err
		}

		writePrice, err = zcncore.ConvertUSDToToken(writePrice)
		if err != nil {
			return nil, err
		}
	}
	sn.Terms.ReadPrice = zcncore.ConvertToValue(readPrice)
	sn.Terms.WritePrice = zcncore.ConvertToValue(writePrice)
	sn.Terms.MinLockDemand = config.Configuration.MinLockDemand
	sn.Terms.MaxOfferDuration = config.Configuration.MaxOfferDuration
	sn.Terms.ChallengeCompletionTime = config.Configuration.ChallengeCompletionTime

	sn.StakePoolSettings.DelegateWallet = config.Configuration.DelegateWallet
	sn.StakePoolSettings.MinStake = config.Configuration.MinStake
	sn.StakePoolSettings.MaxStake = config.Configuration.MaxStake
	sn.StakePoolSettings.NumDelegates = config.Configuration.NumDelegates
	sn.StakePoolSettings.ServiceCharge = config.Configuration.ServiceCharge
	return sn, nil
}
