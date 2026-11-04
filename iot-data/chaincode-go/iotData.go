package main

import (
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
	"github.com/hyperledger/fabric-samples/iot-data/chaincode-go/chaincode"
)

func main() {
	iotChaincode, err := contractapi.NewChaincode(new(chaincode.SmartContract))
	if err != nil {
		log.Panicf("Error creating iot-data chaincode: %v", err)
	}

	if err := iotChaincode.Start(); err != nil {
		log.Panicf("Error starting iot-data chaincode: %v", err)
	}
}
