package tracker

import (
	"context"
	"fmt"
	"log"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	tellorCommon "github.com/tellor-io/TellorMiner/common"
	"github.com/tellor-io/TellorMiner/config"
	"github.com/tellor-io/TellorMiner/db"
	"github.com/tellor-io/TellorMiner/rpc"
)

//BalanceTracker concrete tracker type
type BalanceTracker struct {
}

func (b *BalanceTracker) String() string {
	return "BalanceTracker"
}

//Exec implementation for tracker
func (b *BalanceTracker) Exec(ctx context.Context) error {

	//cast client using type assertion since context holds generic interface{}
	client := ctx.Value(tellorCommon.ClientContextKey).(rpc.ETHClient)
	DB := ctx.Value(tellorCommon.DBContextKey).(db.DB)

	//get the single config instance
	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatal(err)
		return err
	}

	//get address from config
	_fromAddress := cfg.PublicAddress

	//convert to address
	fromAddress := common.HexToAddress(_fromAddress)

	fmt.Printf("Retrieving balance for %v\n", _fromAddress)

	balance, err := client.BalanceAt(ctx, fromAddress, nil)

	if err != nil {
		log.Fatal(err)
		return err
	}
	fmt.Printf("Balance retrieved: %v\n", balance)
	enc := hexutil.EncodeBig(balance)
	log.Printf("Balance: %v", enc)
	return DB.Put(db.BalanceKey, []byte(enc))
}