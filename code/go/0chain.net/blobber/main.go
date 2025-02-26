package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"0chain.net/blobbercore/allocation"
	"0chain.net/blobbercore/challenge"
	"0chain.net/blobbercore/config"
	"0chain.net/blobbercore/datastore"
	"0chain.net/blobbercore/filestore"
	"0chain.net/blobbercore/handler"
	"0chain.net/blobbercore/readmarker"
	"0chain.net/blobbercore/writemarker"
	"0chain.net/core/build"
	"0chain.net/core/chain"
	"0chain.net/core/common"
	"0chain.net/core/encryption"
	"0chain.net/core/logging"
	. "0chain.net/core/logging"
	"0chain.net/core/node"
	"0chain.net/core/transaction"
	"0chain.net/core/util"

	"github.com/0chain/gosdk/zcncore"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

//var BLOBBER_REGISTERED_LOOKUP_KEY = datastore.ToKey("blobber_registration")

var startTime time.Time
var serverChain *chain.Chain
var filesDir *string
var metadataDB *string

func initHandlers(r *mux.Router) {
	r.HandleFunc("/", HomePageHandler)
	handler.SetupHandlers(r)
}

func SetupWorkerConfig() {
	config.Configuration.ContentRefWorkerFreq = viper.GetInt64("contentref_cleaner.frequency")
	config.Configuration.ContentRefWorkerTolerance = viper.GetInt64("contentref_cleaner.tolerance")

	config.Configuration.OpenConnectionWorkerFreq = viper.GetInt64("openconnection_cleaner.frequency")
	config.Configuration.OpenConnectionWorkerTolerance = viper.GetInt64("openconnection_cleaner.tolerance")

	config.Configuration.WMRedeemFreq = viper.GetInt64("writemarker_redeem.frequency")
	config.Configuration.WMRedeemNumWorkers = viper.GetInt("writemarker_redeem.num_workers")

	config.Configuration.RMRedeemFreq = viper.GetInt64("readmarker_redeem.frequency")
	config.Configuration.RMRedeemNumWorkers = viper.GetInt("readmarker_redeem.num_workers")

	config.Configuration.ChallengeResolveFreq = viper.GetInt64("challenge_response.frequency")
	config.Configuration.ChallengeResolveNumWorkers = viper.GetInt("challenge_response.num_workers")
	config.Configuration.ChallengeMaxRetires = viper.GetInt("challenge_response.max_retries")

	config.Configuration.ColdStorageMinimumFileSize = viper.GetInt64("cold_storage.min_file_size")
	config.Configuration.ColdStorageTimeLimitInHours = viper.GetInt64("cold_storage.file_time_limit_in_hours")
	config.Configuration.ColdStorageJobQueryLimit = viper.GetInt64("cold_storage.job_query_limit")
	config.Configuration.ColdStorageStartCapacitySize = viper.GetInt64("cold_storage.start_capacity_size")
	config.Configuration.ColdStorageDeleteLocalCopy = viper.GetBool("cold_storage.delete_local_copy")
	config.Configuration.ColdStorageDeleteCloudCopy = viper.GetBool("cold_storage.delete_cloud_copy")

	config.Configuration.MinioStart = viper.GetBool("minio.start")
	config.Configuration.MinioWorkerFreq = viper.GetInt64("minio.worker_frequency")
	config.Configuration.MinioUseSSL = viper.GetBool("minio.use_ssl")

	config.Configuration.Capacity = viper.GetInt64("capacity")
	config.Configuration.MaxFileSize = viper.GetInt64("max_file_size")

	config.Configuration.DBHost = viper.GetString("db.host")
	config.Configuration.DBName = viper.GetString("db.name")
	config.Configuration.DBPort = viper.GetString("db.port")
	config.Configuration.DBUserName = viper.GetString("db.user")
	config.Configuration.DBPassword = viper.GetString("db.password")

	config.Configuration.Capacity = viper.GetInt64("capacity")
	config.Configuration.ReadPrice = viper.GetFloat64("read_price")
	config.Configuration.WritePrice = viper.GetFloat64("write_price")
	config.Configuration.PriceInUSD = viper.GetBool("price_in_usd")
	config.Configuration.MinLockDemand = viper.GetFloat64("min_lock_demand")
	config.Configuration.MaxOfferDuration = viper.GetDuration("max_offer_duration")
	config.Configuration.ChallengeCompletionTime = viper.GetDuration("challenge_completion_time")

	config.Configuration.ReadLockTimeout = int64(
		viper.GetDuration("read_lock_timeout") / time.Second,
	)
	config.Configuration.WriteLockTimeout = int64(
		viper.GetDuration("write_lock_timeout") / time.Second,
	)

	config.Configuration.UpdateAllocationsInterval =
		viper.GetDuration("update_allocations_interval")

	config.Configuration.DelegateWallet = viper.GetString("delegate_wallet")
	if w := config.Configuration.DelegateWallet; len(w) != 64 {
		log.Fatal("invalid delegate wallet:", w)
	}
	config.Configuration.MinStake = int64(viper.GetFloat64("min_stake") * 1e10)
	config.Configuration.MaxStake = int64(viper.GetFloat64("max_stake") * 1e10)
	config.Configuration.NumDelegates = viper.GetInt("num_delegates")
	config.Configuration.ServiceCharge = viper.GetFloat64("service_charge")
}

func SetupWorkers() {
	var root = common.GetRootContext()
	handler.SetupWorkers(root)
	challenge.SetupWorkers(root)
	readmarker.SetupWorkers(root)
	writemarker.SetupWorkers(root)
	allocation.StartUpdateWorker(root,
		config.Configuration.UpdateAllocationsInterval)
	// stats.StartEventDispatcher(2)
}

var fsStore filestore.FileStore //nolint:unused // global which might be needed somewhere

func initEntities() (err error) {
	fsStore, err = filestore.SetupFSStore(*filesDir + "/files")
	return err
}

func initServer() {

}

func checkForDBConnection() {
	retries := 0
	var err error
	for retries < 600 {
		err = datastore.GetStore().Open()
		if err != nil {
			time.Sleep(1 * time.Second)
			retries++
			continue
		}
		break
	}

	if err != nil {
		Logger.Error("Error in opening the database. Shutting the server down")
		panic(err)
	}
}

func processMinioConfig(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	more := scanner.Scan()
	if !more {
		return common.NewError("process_minio_config_failed", "Unable to read minio config from minio config file")
	}

	filestore.MinioConfig.StorageServiceURL = scanner.Text()
	more = scanner.Scan()
	if !more {
		return common.NewError("process_minio_config_failed", "Unable to read minio config from minio config file")
	}

	filestore.MinioConfig.AccessKeyID = scanner.Text()
	more = scanner.Scan()
	if !more {
		return common.NewError("process_minio_config_failed", "Unable to read minio config from minio config file")
	}

	filestore.MinioConfig.SecretAccessKey = scanner.Text()
	more = scanner.Scan()
	if !more {
		return common.NewError("process_minio_config_failed", "Unable to read minio config from minio config file")
	}

	filestore.MinioConfig.BucketName = scanner.Text()
	more = scanner.Scan()
	if !more {
		return common.NewError("process_minio_config_failed", "Unable to read minio config from minio config file")
	}

	filestore.MinioConfig.BucketLocation = scanner.Text()
	return nil
}

// // Comment out to pass lint. Still keep this function around in case we want to
// // change how CORS validates origins.
// func isValidOrigin(origin string) bool {
// 	var url, err = url.Parse(origin)
// 	if err != nil {
// 		return false
// 	}
// 	var host = url.Hostname()
// 	if host == "localhost" {
// 		return true
// 	}
// 	if host == "0chain.net" || host == "0box.io" ||
// 		strings.HasSuffix(host, ".0chain.net") ||
// 		strings.HasSuffix(host, ".alphanet-0chain.net") ||
// 		strings.HasSuffix(host, ".testnet-0chain.net") ||
// 		strings.HasSuffix(host, ".devnet-0chain.net") ||
// 		strings.HasSuffix(host, ".mainnet-0chain.net") {
// 		return true
// 	}
// 	return false
// }

func main() {
	deploymentMode := flag.Int("deployment_mode", 2, "deployment_mode")
	keysFile := flag.String("keys_file", "", "keys_file")
	minioFile := flag.String("minio_file", "", "minio_file")
	filesDir = flag.String("files_dir", "", "files_dir")
	metadataDB = flag.String("db_dir", "", "db_dir")
	logDir := flag.String("log_dir", "", "log_dir")
	portString := flag.String("port", "", "port")
	grpcPortString := flag.String("grpc_port", "", "grpc_port")
	hostname := flag.String("hostname", "", "hostname")

	flag.Parse()

	config.SetupDefaultConfig()
	config.SetupConfig()

	config.Configuration.DeploymentMode = byte(*deploymentMode)

	if config.Development() {
		logging.InitLogging("development", *logDir, "0chainBlobber.log")
	} else {
		logging.InitLogging("production", *logDir, "0chainBlobber.log")
	}
	config.Configuration.ChainID = viper.GetString("server_chain.id")
	config.Configuration.SignatureScheme = viper.GetString("server_chain.signature_scheme")
	SetupWorkerConfig()

	if *filesDir == "" {
		panic("Please specify --files_dir absolute folder name option where uploaded files can be stored")
	}

	if *metadataDB == "" {
		panic("Please specify --db_dir absolute folder name option where meta data db can be stored")
	}

	if *hostname == "" {
		panic("Please specify --hostname which is the public hostname")
	}

	if *portString == "" {
		panic("Please specify --port which is the port on which requests are accepted")
	}

	if *grpcPortString == "" {
		panic("Please specify --grpc_port which is the grpc port on which requests are accepted")
	}

	reader, err := os.Open(*keysFile)
	if err != nil {
		panic(err)
	}

	publicKey, privateKey, _, _ := encryption.ReadKeys(reader)
	reader.Close()

	reader, err = os.Open(*minioFile)
	if err != nil {
		panic(err)
	}

	err = processMinioConfig(reader)
	if err != nil {
		panic(err)
	}
	reader.Close()

	node.Self.SetKeys(publicKey, privateKey)

	port, err := strconv.Atoi(*portString) //fmt.Sprintf(":%v", port) // node.Self.Port
	if err != nil {
		Logger.Panic("Port specified is not Int " + *portString)
		return
	}

	node.Self.SetHostURL(*hostname, port)
	Logger.Info(" Base URL" + node.Self.GetURLBase())

	config.SetServerChainID(config.Configuration.ChainID)

	common.SetupRootContext(node.GetNodeContext())
	//ctx := common.GetRootContext()
	serverChain = chain.NewChainFromConfig()

	if node.Self.ID == "" {
		Logger.Panic("node definition for self node doesn't exist")
	} else {
		Logger.Info("self identity", zap.Any("id", node.Self.ID))
	}

	initIntegrationsTests(node.Self.ID)

	//address := publicIP + ":" + portString
	address := ":" + *portString

	chain.SetServerChain(serverChain)

	checkForDBConnection()

	// Initialize after server chain is setup.
	if err := initEntities(); err != nil {
		Logger.Error("Error setting up blobber on blockchian" + err.Error())
	}
	if err := SetupBlobberOnBC(*logDir); err != nil {
		Logger.Error("Error setting up blobber on blockchian" + err.Error())
	}
	mode := "main net"
	if config.Development() {
		mode = "development"
	} else if config.TestNet() {
		mode = "test net"
	}
	Logger.Info("Starting blobber", zap.Int("available_cpus", runtime.NumCPU()), zap.String("port", *portString), zap.String("chain_id", config.GetServerChainID()), zap.String("mode", mode))

	var server *http.Server

	// setup CORS
	r := mux.NewRouter()

	headersOk := handlers.AllowedHeaders([]string{
		"X-Requested-With", "X-App-Client-ID",
		"X-App-Client-Key", "Content-Type",
		"X-App-Client-Signature",
	})

	// Allow anybody to access API.
	// originsOk := handlers.AllowedOriginValidator(isValidOrigin)
	originsOk := handlers.AllowedOrigins([]string{"*"})

	methodsOk := handlers.AllowedMethods([]string{"GET", "HEAD", "POST", "PUT",
		"DELETE", "OPTIONS"})

	rl := common.ConfigRateLimits()
	initHandlers(r)
	initServer()

	grpcServer := handler.NewServerWithMiddlewares(rl)
	handler.RegisterGRPCServices(r, grpcServer)

	rHandler := handlers.CORS(originsOk, headersOk, methodsOk)(r)
	if config.Development() {
		// No WriteTimeout setup to enable pprof
		server = &http.Server{
			Addr:              address,
			ReadHeaderTimeout: 30 * time.Second,
			MaxHeaderBytes:    1 << 20,
			Handler:           rHandler,
		}
	} else {
		server = &http.Server{
			Addr:              address,
			ReadHeaderTimeout: 30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       30 * time.Second,
			MaxHeaderBytes:    1 << 20,
			Handler:           rHandler,
		}
	}
	common.HandleShutdown(server)
	handler.HandleShutdown(common.GetRootContext())

	Logger.Info("Ready to listen to the requests")
	startTime = time.Now().UTC()
	go func(grpcPort string) {
		lis, err := net.Listen("tcp", fmt.Sprintf(":%s", grpcPort))
		if err != nil {
			log.Fatalf("failed to listen: %v", err)
		}
		log.Fatal(grpcServer.Serve(lis))
	}(*grpcPortString)
	log.Fatal(server.ListenAndServe())
}

func RegisterBlobber() {
	setup := func() {
		// badgerdbstore.GetStorageProvider().WriteBytes(ctx, BLOBBER_REGISTERED_LOOKUP_KEY, []byte(txnHash))
		// badgerdbstore.GetStorageProvider().Commit(ctx)
		SetupWorkers()
		go BlobberHealthCheck()
		if config.Configuration.PriceInUSD {
			go UpdateBlobberSettings()
		}
	}

	registrationRetries := 0
	// ctx := badgerdbstore.GetStorageProvider().WithConnection(common.GetRootContext())
	for registrationRetries < 10 {
		txnHash, err := handler.RegisterBlobber(common.GetRootContext())
		if err == handler.ErrBlobberHasRegistered {
			Logger.Debug("Blobber already registered to the mining network")
			setup()
			return
		}

		time.Sleep(transaction.SLEEP_FOR_TXN_CONFIRMATION * time.Second)
		txnVerified := false
		verifyRetries := 0
		for verifyRetries < util.MAX_RETRIES {
			time.Sleep(transaction.SLEEP_FOR_TXN_CONFIRMATION * time.Second)
			t, err := transaction.VerifyTransaction(txnHash, chain.GetServerChain())
			if err == nil {
				Logger.Info("Transaction for adding blobber accepted and verified", zap.String("txn_hash", t.Hash), zap.Any("txn_output", t.TransactionOutput))
				setup()
				return
			}
			verifyRetries++
		}

		if !txnVerified {
			Logger.Error("Add blobber transaction could not be verified", zap.Any("err", err), zap.String("txn.Hash", txnHash))
		}
	}
}

func BlobberHealthCheck() {
	const HEALTH_CHECK_TIMER = 60 * 15 // 15 Minutes
	for {
		txnHash, err := handler.BlobberHealthCheck(common.GetRootContext())
		if err != nil && err == handler.ErrBlobberHasRemoved {
			time.Sleep(HEALTH_CHECK_TIMER * time.Second)
			continue
		}
		time.Sleep(transaction.SLEEP_FOR_TXN_CONFIRMATION * time.Second)
		txnVerified := false
		verifyRetries := 0
		for verifyRetries < util.MAX_RETRIES {
			time.Sleep(transaction.SLEEP_FOR_TXN_CONFIRMATION * time.Second)
			t, err := transaction.VerifyTransaction(txnHash, chain.GetServerChain())
			if err == nil {
				txnVerified = true
				Logger.Info("Transaction for blobber health check verified", zap.String("txn_hash", t.Hash), zap.Any("txn_output", t.TransactionOutput))
				break
			}
			verifyRetries++
		}

		if !txnVerified {
			Logger.Error("Blobber health check transaction could not be verified", zap.Any("err", err), zap.String("txn.Hash", txnHash))
		}
		time.Sleep(HEALTH_CHECK_TIMER * time.Second)
	}
}

func UpdateBlobberSettings() {
	var UPDATE_SETTINGS_TIMER = 60 * 60 * time.Duration(viper.GetInt("price_worker_in_hours"))
	time.Sleep(UPDATE_SETTINGS_TIMER * time.Second)
	for {
		txnHash, err := handler.UpdateBlobberSettings(common.GetRootContext())
		if err != nil {
			time.Sleep(UPDATE_SETTINGS_TIMER * time.Second)
			continue
		}
		time.Sleep(transaction.SLEEP_FOR_TXN_CONFIRMATION * time.Second)
		txnVerified := false
		verifyRetries := 0
		for verifyRetries < util.MAX_RETRIES {
			time.Sleep(transaction.SLEEP_FOR_TXN_CONFIRMATION * time.Second)
			t, err := transaction.VerifyTransaction(txnHash, chain.GetServerChain())
			if err == nil {
				txnVerified = true
				Logger.Info("Transaction for blobber update settings verified", zap.String("txn_hash", t.Hash), zap.Any("txn_output", t.TransactionOutput))
				break
			}
			verifyRetries++
		}

		if !txnVerified {
			Logger.Error("Blobber update settings transaction could not be verified", zap.Any("err", err), zap.String("txn.Hash", txnHash))
		}
		time.Sleep(UPDATE_SETTINGS_TIMER * time.Second)
	}
}

func SetupBlobberOnBC(logDir string) error {
	var logName = logDir + "/0chainBlobber.log"
	zcncore.SetLogFile(logName, false)
	zcncore.SetLogLevel(3)
	if err := zcncore.InitZCNSDK(serverChain.BlockWorker, config.Configuration.SignatureScheme); err != nil {
		return err
	}
	if err := zcncore.SetWalletInfo(node.Self.GetWalletString(), false); err != nil {
		return err
	}
	go RegisterBlobber()
	return nil
}

/*HomePageHandler - provides basic info when accessing the home page of the server */
func HomePageHandler(w http.ResponseWriter, r *http.Request) {
	mc := chain.GetServerChain()
	fmt.Fprintf(w, "<div>Running since %v ...\n", startTime)
	fmt.Fprintf(w, "<div>Working on the chain: %v</div>\n", mc.ID)
	fmt.Fprintf(w, "<div>I am a blobber with <ul><li>id:%v</li><li>public_key:%v</li><li>build_tag:%v</li></ul></div>\n", node.Self.ID, node.Self.PublicKey, build.BuildTag)
	fmt.Fprintf(w, "<div>Miners ...\n")
	network := zcncore.GetNetwork()
	for _, miner := range network.Miners {
		fmt.Fprintf(w, "%v\n", miner)
	}
	fmt.Fprintf(w, "<div>Sharders ...\n")
	for _, sharder := range network.Sharders {
		fmt.Fprintf(w, "%v\n", sharder)
	}
}
