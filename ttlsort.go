package main

import (
	"flag"
	"net"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type echoReply struct {
	OriginalTTL int
	From        net.Addr
}

func main() {
	log.SetLevel(log.InfoLevel)

	targetHost, maxNumIters, antiFloodChill, toSort := fromParams()
	chillDuration := time.Duration(antiFloodChill) * time.Second
	log.Infoln("Will sort", toSort, "at most", maxNumIters, "times and sleep for", chillDuration, "between iterations.")

	maxVal := findMax(toSort)
	dstAddr := lookupTargetAddr(targetHost)
	isPerfectlySorted := false
	hopsToTarget := -1 // -1 means we haven't encountered dst host

	for i := 0; i < maxNumIters; i++ {
		toSort, hopsToTarget = performSort(dstAddr, toSort)
		log.Info("Current sorted array: ", toSort)

		if hopsToTarget >= 0 && maxVal > hopsToTarget {
			log.Error("We are sorting elements with values higher than distance to target host (", hopsToTarget, "hops). Try adjusting the --target to be more hops away.")
			break
		}

		if isPerfectlySorted = isSorted(toSort); isPerfectlySorted {
			break
		}

		log.Info("Chilling for ", chillDuration, " (anti-flood detection)")
		time.Sleep(chillDuration)
	}

	if isPerfectlySorted {
		log.Info("Et voilà! ", toSort)
	} else {
		log.Info("Final result (try increasing --iters if you're not satisfied): ", toSort)
	}
}

func fromParams() (string, int, int, []int) {
	targetHost := flag.String("target", "www.baidu.com", "target to use for sorting")
	maxNumIters := flag.Int("iters", 3, "will perform sort up to this number of times (or until sorted) - more iters increase sorting confidence)")
	antiFloodChill := flag.Int("chill", 3, "how long to sleep between sorts in seconds (anti-flood detection)")
	flag.Parse()

	toSortStr := flag.Args()

	toSort := []int{}
	for _, val := range toSortStr {
		intVal, err := strconv.Atoi(val)
		if err != nil {
			log.Fatal(err)
		}
		toSort = append(toSort, intVal)
	}
	return *targetHost, *maxNumIters, *antiFloodChill, toSort
}

func lookupTargetAddr(target string) net.IPAddr {
	ips, err := net.LookupIP(target)
	if err != nil {
		log.Fatal(err)
	}

	var ipAddr net.IPAddr

	for _, ip := range ips {
		if ip.To4() != nil {
			ipAddr.IP = ip
			log.Info("Will send echo requests to", ipAddr.IP)
			break
		}
	}
	if ipAddr.IP == nil {
		log.Fatal("Couldn't lookup", target)
	}

	return ipAddr
}

func newEchoConn() *ipv4.PacketConn {
	rawPacketConn, err := net.ListenPacket("ip4:1", "0.0.0.0") // ICMP for IPv4
	if err != nil {
		log.Fatal(err)
	}

	ipv4Conn := ipv4.NewPacketConn(rawPacketConn)
	if err := ipv4Conn.SetControlMessage(ipv4.FlagTTL|ipv4.FlagSrc|ipv4.FlagDst|ipv4.FlagInterface, true); err != nil {
		log.Fatal(err)
	}

	if err := ipv4Conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		log.Fatal(err)
	}

	return ipv4Conn
}

func performSort(targetAddr net.IPAddr, toSort []int) ([]int, int) {
	conn := newEchoConn()
	defer conn.Close()

	sortedChan := make(chan echoReply, len(toSort))

	trackArr := toSort

	go listenAndSort(conn, len(toSort), sortedChan)
	for _, val := range toSort {
		go sendPing(targetAddr, val, conn)
	}

	sorted := []int{}
	hopsToTarget := -1

	for i := 0; i < len(toSort); i++ {
		echoReply := <-sortedChan
		id := echoReply.OriginalTTL
		sorted = append(sorted, id)

		isOriginalDst := targetAddr.String() == echoReply.From.String()
		log.Debug("isOriginalDst=", isOriginalDst, "(", targetAddr.String(), ", ", echoReply.From.String(), ")")
		if isOriginalDst {
			hopsToTarget = echoReply.OriginalTTL
		}

		// below is mostly for debug
		for j := 0; j < len(trackArr); j++ {
			if id == trackArr[j] {
				trackArr = remove(trackArr, j)
			}
		}
		log.Debugln("Still waiting for ", trackArr)
	}

	return sorted, hopsToTarget
}

func isSorted(arr []int) bool {
	// this check is only O(n), don't worry
	for i := 0; i < len(arr)-1; i++ {
		if arr[i] > arr[i+1] {
			return false
		}
	}
	return true
}

func listenAndSort(conn *ipv4.PacketConn, numExpected int, replyChan chan echoReply) {
	for i := 0; i < numExpected; i++ {
		readBuffer := make([]byte, 1500)

		n, _, peer, err := conn.ReadFrom(readBuffer)
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Timeout() {
				log.Print("failed to read ", err)
			}
		}

		rm, err := icmp.ParseMessage(1, readBuffer[:n])
		if err != nil {
			log.Fatal("failed to parse ", err, readBuffer[:n])
		}

		switch rm.Type {
		case ipv4.ICMPTypeTimeExceeded:
			rawbody := rm.Body.(*icmp.TimeExceeded).Data
			id := rawbody[25]
			log.Infof("Time exceeded\tID=%v\t%v\n", id, peer)
			replyChan <- echoReply{OriginalTTL: fromIDtoTTL(int(id)), From: peer}

		case ipv4.ICMPTypeEchoReply:
			id := rm.Body.(*icmp.Echo).ID
			log.Infof("Echo reply\t\tID=%v\t%v\n", id, peer)
			replyChan <- echoReply{OriginalTTL: fromIDtoTTL(int(id)), From: peer}

		default:
			log.Fatal("unknown ICMP message ", rm)
		}
	}
}

func sendPing(dst net.IPAddr, ttl int, conn *ipv4.PacketConn) {
	newConn := newEchoConn()
	defer newConn.Close()

	wm := icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{
			ID:   fromTTLtoID(ttl),
			Data: []byte("don't mind me"),
		},
	}

	wb, err := wm.Marshal(nil)
	if err != nil {
		log.Fatal(err)
	}

	if err := newConn.SetTTL(ttl); err != nil {
		log.Fatal(err)
	}

	if _, err := newConn.WriteTo(wb, nil, &dst); err != nil {
		log.Fatal(err)
	}
}

func remove(slice []int, i int) []int {
	copy(slice[i:], slice[i+1:])
	return slice[:len(slice)-1]
}

func findMax(slice []int) int {
	currentMax := 0
	for _, val := range slice {
		if val > currentMax {
			currentMax = val
		}
	}
	return currentMax
}

func fromTTLtoID(ttl int) int {
	return ttl + 100
}

func fromIDtoTTL(id int) int {
	return id - 100
}
