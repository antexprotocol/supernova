package meter_test

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/meterio/meter-pov/meter"
)

func TestBucket(t *testing.T) {
	bkts := make([]*meter.Bucket, 0)
	addr := meter.MustParseAddress("0x08ebea6584b3d9bf6fbcacf1a1507d00a61d95b7")
	b1 := meter.NewBucket(addr, addr, big.NewInt(100), 1, 0, 0, 0, 1, 1)
	b2 := meter.NewBucket(addr, addr, big.NewInt(200), 1, 0, 0, 0, 2, 2)
	b3 := meter.NewBucket(addr, addr, big.NewInt(300), 1, 0, 0, 0, 3, 3)
	b4 := meter.NewBucket(addr, addr, big.NewInt(400), 1, 0, 0, 0, 4, 4)
	b5 := meter.NewBucket(addr, addr, big.NewInt(500), 1, 0, 0, 0, 5, 5)
	b6 := meter.NewBucket(addr, addr, big.NewInt(600), 1, 0, 0, 0, 6, 6)

	bkts = append(bkts, b1, b2, b3, b4, b5, b6)
	list := meter.NewBucketList(bkts)

	for i := 0; i < len(list.Buckets); i++ {
		fmt.Println("i =", i)
		for j, b := range list.Buckets {
			fmt.Print("[", j, "]", b.Value, ",")
		}

		fmt.Println("")
		b := list.Buckets[i]
		fmt.Println("visit bucket #", i, b.Value)
		if b.Value.Cmp(big.NewInt(100)) == 0 || b.Value.Cmp(big.NewInt(400)) == 0 || b.Value.Cmp(big.NewInt(300)) == 0 {
			fmt.Println("remove bucket:", b.Value)
			list.Remove(b.ID())
			i--
		}
		fmt.Println("--------------------------------")
	}
}
