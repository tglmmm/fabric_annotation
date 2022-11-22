/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blockcutter

import "github.com/hyperledger/fabric/common/metrics"

var blockFillDuration = metrics.HistogramOpts{
	Namespace: "blockcutter",
	Name:      "block_fill_duration",
	// 从第一个交易到区块被打包时候花费时间
	Help:         "The time from first transaction enqueing to the block being cut in seconds.",
	LabelNames:   []string{"channel"},
	StatsdFormat: "%{#fqname}.%{channel}",
}

type Metrics struct {
	BlockFillDuration metrics.Histogram
}

func NewMetrics(p metrics.Provider) *Metrics {
	return &Metrics{
		BlockFillDuration: p.NewHistogram(blockFillDuration),
	}
}

// 自己创建的Metrics
// 记录超过总数PreferredMaxBytes 单独被打包的区块
var blockMoreThanPreferredMaxBytesCount = metrics.CounterOpts{
	Namespace:    "blockcutter",
	Name:         "envelop_morethan_PreferredMaxBytes",
	Help:         "The amount of envelop more PreferredMaxBytes",
	LabelNames:   []string{"channel"},
	StatsdFormat: "%{#fqname}.%{channel}",
}

func NewBlockMorethanPreferred(p metrics.Provider) metrics.Counter {
	return p.NewCounter(blockMoreThanPreferredMaxBytesCount)
}
