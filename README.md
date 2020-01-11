# Go-TTLSort

Cloud-native sorting algorithm, written in Go

## Usage

```
sudo ./ttlsort --target [host] --iters [num_iters] --chill [chill_time_in_sec] [numbers_to_sort...]
```

Example. Sort numbers {10, 5, 3, 6, 3, 6}, re-sorting ta most 5 times, with 1 second chill time
to prevent being blocked by an anti-flood filter, using www.baidu.com as echo request target.

```
sudo ./ttlsort --target www.baidu.com --iters 5 --chill 1 10 5 3 6 3 6
```

## How does it work

It's basically an implementation of sleepsort algorithm but using ICMP echo requests with adjusted TTL (time-to-live)
instead of `sleep` function.

## Limitations

It's basically a race condition. Moreover, network is not reliable and packets may arrive in arbitrary order.
To mitigate this, multiple sorts will be performed, each one using the partially sorted result of the one before.
After each iteration, the array is checked if it's already sorted, since it's computationally less expensive than
the sorting itself.

We must be careful not to be blocked for ICMP flood, so there is a "chill" time after each iteration.

Moreover, we cannot sort arrays with negative numbers. More importantly, we cannot sort elements whose values are
higher than the number of hops to destination target of our echo requests. The program will warn about this when
such scenario is encountered.

One last thing. To send ICMP messages, we need sudo, or CAP_NET_RAW capabilities.
