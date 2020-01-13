package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/tellor-io/TellorMiner/cli"
	tellorCommon "github.com/tellor-io/TellorMiner/common"
	"github.com/tellor-io/TellorMiner/config"
	"github.com/tellor-io/TellorMiner/contracts"
	"github.com/tellor-io/TellorMiner/contracts1"
	"github.com/tellor-io/TellorMiner/db"
	"github.com/tellor-io/TellorMiner/ops"
	"github.com/tellor-io/TellorMiner/rpc"
	"github.com/tellor-io/TellorMiner/util"
	"log"
	"os"
	"os/signal"
	"runtime"
	"time"
)

var mainLog = util.NewLogger("main", "Main")


func main() {

	//create os kill sig listener
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)

	//see what args are passed in
	globalFlags := cli.GetFlags()

	//set things up
	config.ParseConfig(globalFlags.ConfigPath)
	util.ParseLoggingConfig(globalFlags.LoggingConfigPath)

	//global config
	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatal(err)
	}

	runtime.GOMAXPROCS(cfg.NumProcessors)


	//create a db instance
	os.RemoveAll(cfg.DBFile)
	DB, err := db.Open(cfg.DBFile)
	if err != nil {
		log.Fatal(err)
	}

	//create an rpc client
	client, err := rpc.NewClient(cfg.NodeURL)
	if err != nil {
		log.Fatal(err)
	}
	//create an instance of the tellor master contract for on-chain interactions
	contractAddress := common.HexToAddress(cfg.ContractAddress)
	masterInstance, err := contracts.NewTellorMaster(contractAddress, client)
	transactorInstance, err := contracts1.NewTellorTransactor(contractAddress, client)
	if err != nil {
		log.Fatal(err)
	}

	var ds *ops.DataServerOps
	//var miner *ops.MinerOps
	var miner *ops.MiningMgr

	var dataProxy db.DataServerProxy
	if globalFlags.DataServer && globalFlags.Miner {
		proxy, err := db.OpenLocalProxy(DB)
		if err != nil {
			log.Fatal(err)
		}
		dataProxy = proxy
	} else {
		proxy, err := db.OpenRemoteDB(DB)
		if err != nil {
			log.Fatal(err)
		}
		dataProxy = proxy
	}

	//create a context to use for ops
	ctx := context.WithValue(context.Background(), tellorCommon.DBContextKey, DB)
	ctx = context.WithValue(ctx, tellorCommon.ClientContextKey, client)
	ctx = context.WithValue(ctx, tellorCommon.MasterContractContextKey, masterInstance)
	ctx = context.WithValue(ctx, tellorCommon.TransactorContractContextKey, transactorInstance)
	ctx = context.WithValue(ctx, tellorCommon.DataProxyKey, dataProxy)

	//Issue #55, halt if client is still syncing with Ethereum network
	s, err := client.IsSyncing(ctx)
	if err != nil {
		log.Fatalf("Could not determine if Ethereum client is syncing: %v\n", err)
	}
	if s {
		log.Fatal("Ethereum node is still sycning with the network")
	}

	//everything left over from the earlier parsing
	cmdArgs := flag.Args()

	if len(cmdArgs) == 0 {
		cli.Help(ctx)
		return
	}
	cmdName := cmdArgs[0]
	cmd, ok := cli.Commands[cmdName]
	if !ok {
		fmt.Fprintf(os.Stderr, "'%s' is not a valid command. For a list, try\n\t%s help\n", cmdName, os.Args[0])
	}
	err = cmd.Options.Parse(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid arguments to %s: %s\n", cmdName, err.Error())
		fmt.Fprintf(os.Stderr, "Usage:\n")
		cmd.Options.PrintDefaults()
		return
	}
	err = cmd.Cmd(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to execute %s: %s\n", cmdName, err.Error())
		return
	}
	return

	err = ops.Activity(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "activity failed: %s\n", err.Error())
	}

	exitChannels := make([]*chan os.Signal, 0)

	if globalFlags.Transfer {
		ops.Transfer(globalFlags.ToAddress, globalFlags.Amount, ctx)
	} else if globalFlags.Deposit {
		ops.Deposit(ctx)
	} else if globalFlags.Approve {
		ops.Approve(globalFlags.ToAddress, globalFlags.Amount, ctx)
	} else if globalFlags.Dispute {
		ops.Dispute(globalFlags.RequestId, globalFlags.Timestamp, globalFlags.MinerIndex, ctx)
	} else if globalFlags.RequestStakingWithdraw {
		ops.RequestStakingWithdraw(ctx)
	} else if globalFlags.WithdrawStake {
		ops.WithdrawStake(ctx)
	} else if globalFlags.Vote {
		ops.Vote(globalFlags.DisputeId, globalFlags.SupportsDispute, ctx)
	} else {
		if globalFlags.DataServer {
			ch := make(chan os.Signal)
			exitChannels = append(exitChannels, &ch)
			ds, err = ops.CreateDataServerOps(ctx, ch)
			if err != nil {
				log.Fatal(err)
			}
		}

		if globalFlags.Miner {
			ch := make(chan os.Signal)
			exitChannels = append(exitChannels, &ch)
			miner, err = ops.CreateMiningManager(ctx, ch, ops.NewSubmitter())
			if err != nil {
				log.Fatal(err)
			}
		}

		if ds != nil {
			//start the data server
			ds.Start(ctx)
		}

		if miner != nil {
			//start the miner after at least one cycle from the data server, if it's running
			if ds != nil {
				<-ds.Ready()
			}

			miner.Start(ctx)
		}

		//now we wait for kill sig
		<-c
		//and then notify exit channels
		for _, ch := range exitChannels {
			*ch <- os.Interrupt
		}
		cnt := 0
		start := time.Now()
		for {
			cnt++
			dsStopped := false
			minerStopped := false

			if ds != nil {
				dsStopped = !ds.Running
			} else {
				dsStopped = true
			}

			if miner != nil {
				minerStopped = !miner.Running
			} else {
				minerStopped = true
			}

			if !dsStopped && !minerStopped && cnt > 60 {
				mainLog.Warn("Taking longer than expected to operations. Waited %v so far", time.Now().Sub(start))
			} else if dsStopped && minerStopped {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	mainLog.Info("Main shutdown complete")
}
