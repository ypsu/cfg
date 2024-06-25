package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestNext(t *testing.T) {
	kvs := strings.Split(data, "===")
	for i := 0; i < len(kvs); i += 2 {
		input, want := kvs[i], strings.TrimSpace(kvs[i+1])
		if got := fmt.Sprint(next(input)); got != want {
			t.Errorf("next(%dth input):\n got: %s\nwant: %s", i/2, got, want)
		}
	}
}

const data = `

test input 0:
Screen 0: minimum 320 x 200, current 2560 x 1440, maximum 16384 x 16384
DP-1 disconnected primary (normal left inverted right x axis y axis)
DP-2 connected (normal left inverted right x axis y axis)
   2200x1650     40.00 +
   1600x1200     60.00
HDMI-2 disconnected (normal left inverted right x axis y axis)
DP-3 connected 2560x1440+0+0 (normal left inverted right x axis y axis) 609mm x 349mm
   2560x1440     59.95*+
   1920x1080     60.00    60.00    50.00    59.94
   1680x1050     59.88
HDMI-3 disconnected (normal left inverted right x axis y axis)
===
[--output DP-2 --mode 2200x1650 --output DP-3 --off] <nil>
===

test input 1:
Screen 0: minimum 320 x 200, current 2560 x 1440, maximum 16384 x 16384
DP-1 disconnected primary (normal left inverted right x axis y axis)
DP-2 connected (normal left inverted right x axis y axis)
   2200x1650     40.00*+
   1600x1200     60.00
HDMI-2 disconnected (normal left inverted right x axis y axis)
DP-3 connected 2560x1440+0+0 (normal left inverted right x axis y axis) 609mm x 349mm
   2560x1440     59.95 +
   1920x1080     60.00    60.00    50.00    59.94
   1680x1050     59.88
HDMI-3 disconnected (normal left inverted right x axis y axis)
===
[--output DP-2 --off --output DP-3 --mode 2560x1440] <nil>
===

test input 2:
Screen 0: minimum 320 x 200, current 2560 x 1440, maximum 16384 x 16384
eDP-1 connected primary (normal left inverted right x axis y axis)
   3840x2400     60.00 +
   3840x2160     59.97  
DP-3 connected 2560x1440+0+0 (normal left inverted right x axis y axis) 597mm x 337mm
   2560x1440     59.95*+
   1920x1080     60.00    50.00    59.94  
   1680x1050     59.95  
DP-4 disconnected (normal left inverted right x axis y axis)
  2560x1440 (0xb2) 295.410MHz +HSync -VSync
        h: width  2560 start 2568 end 2600 total 2640 skew    0 clock 111.90KHz
        v: height 1440 start 1478 end 1486 total 1492           clock  75.00Hz

{name:eDP-1 mode:1920x1200 active:false intent:false}
{name:DP-3 mode:1920x1080 active:false intent:true}
===
[--output eDP-1 --mode 1920x1200 --output DP-3 --off] <nil>
===

test input 3:
Screen 0: minimum 320 x 200, current 2560 x 1440, maximum 16384 x 16384
eDP-1 connected primary (normal left inverted right x axis y axis)
   3840x2400     60.00 +
   3840x2160     59.97  
DP-3 connected 2560x1440+0+0 (normal left inverted right x axis y axis) 597mm x 337mm
   1920x1080     60.00    50.00    59.94  
   1680x1050     59.95  
DP-4 disconnected (normal left inverted right x axis y axis)
  2560x1440 (0xb2) 295.410MHz +HSync -VSync
        h: width  2560 start 2568 end 2600 total 2640 skew    0 clock 111.90KHz
        v: height 1440 start 1478 end 1486 total 1492           clock  75.00Hz

{name:eDP-1 mode:1920x1200 active:false intent:false}
{name:DP-3 mode:1920x1080 active:false intent:true}
===
[--output eDP-1 --mode 1920x1200 --output DP-3 --off] <nil>
===

test input 4:
Screen 0: minimum 320 x 200, current 2560 x 1440, maximum 16384 x 16384
DP-3 connected 2560x1440+0+0 (normal left inverted right x axis y axis) 597mm x 337mm
   1920x1080     60.00    50.00    59.94  
   1680x1050     59.95  
eDP-1 connected primary (normal left inverted right x axis y axis)
   3840x2400     60.00 +
   3840x2160     59.97  
DP-4 disconnected (normal left inverted right x axis y axis)
  2560x1440 (0xb2) 295.410MHz +HSync -VSync
        h: width  2560 start 2568 end 2600 total 2640 skew    0 clock 111.90KHz
        v: height 1440 start 1478 end 1486 total 1492           clock  75.00Hz

{name:eDP-1 mode:1920x1200 active:false intent:false}
{name:DP-3 mode:1920x1080 active:false intent:true}
===
[--output DP-3 --off --output eDP-1 --mode 1920x1200] <nil>
===

test input 5:
Screen 0: minimum 320 x 200, current 1920 x 1200, maximum 16384 x 16384
eDP-1 connected primary 1920x1200+0+0 (normal left inverted right x axis y axis) 302mm x 189mm
   3840x2400     60.00 +
   3840x2160     59.97
   2048x1152     59.99    59.98    59.90    59.91
   1920x1200     59.88*   59.95
   1920x1080     60.01    59.97    59.96    59.93
   1600x1200     60.00
HDMI-1 disconnected (normal left inverted right x axis y axis)
DP-1 disconnected (normal left inverted right x axis y axis)
HDMI-2 disconnected (normal left inverted right x axis y axis)
DP-2 disconnected (normal left inverted right x axis y axis)
HDMI-3 disconnected (normal left inverted right x axis y axis)
DP-3 connected (normal left inverted right x axis y axis)
   2560x1440     75.00 +  59.95
   1920x1080     60.00    50.00    59.94
   1680x1050     59.95
DP-4 disconnected (normal left inverted right x axis y axis)
===
[--output eDP-1 --off --output DP-3 --mode 2560x1440] <nil>
`
