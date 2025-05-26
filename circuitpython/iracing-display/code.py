# SPDX-FileCopyrightText: 2021 ladyada for Adafruit Industries
# SPDX-License-Identifier: MIT

"""
Basic display_text.label example script
adapted for use on MagTag.
"""
import time
import board
import displayio
import terminalio
from adafruit_display_text import label

# use built in display (PyPortal, PyGamer, PyBadge, CLUE, etc.)
# see guide for setting up external displays (TFT / OLED breakouts, RGB matrices, etc.)
# https://learn.adafruit.com/circuitpython-display-support-using-displayio/display-and-display-bus
display = board.DISPLAY

# wait until we can draw
time.sleep(display.time_to_refresh)

# main group to hold everything
main_group = displayio.Group()

# white background. Scaled to save RAM
bg_bitmap = displayio.Bitmap(display.width // 8, display.height // 8, 1)
bg_palette = displayio.Palette(1)
bg_palette[0] = 0xFFFFFF
bg_sprite = displayio.TileGrid(bg_bitmap, x=0, y=0, pixel_shader=bg_palette)
bg_group = displayio.Group(scale=8)
bg_group.append(bg_sprite)
main_group.append(bg_group)


practice = {
    "session_type": "Practice",
    "current_lap_number": "2",
    "session_total_laps": "5",
    "session_time_remaining": "5m3s",
    "interrupt": "YES"
}

qualifying = {
    "session_type": "Qualifying",
    "current_lap_number": "2",
    "session_total_laps": "5",
    "session_time_remaining": "5m3s",
    "interrupt": "NO"
}

race = {
    "session_type": "Race",
    "current_lap_number": "20",
    "session_total_laps": "50",
    "session_time_remaining": "5m3s",
    "interrupt": "NO"
}

data = qualifying

### Session type
SESSION_TEXT = f"""Session: {data['session_type'].upper()}
Lap {data['current_lap_number']} of {practice['session_total_laps']}
Remaining: {data['session_time_remaining']}
Interrupt me? {data['interrupt']}"""
session_info_label = label.Label(
    terminalio.FONT,
    text=SESSION_TEXT,
    scale=2,
    color=0x000000,
    padding_right=4,
    padding_left=4,
)

# centered
session_info_label.anchor_point = (0.5, 0.5)
session_info_label.anchored_position = (display.width // 2, display.height // 2)
main_group.append(session_info_label)

# show the main group and refresh.
display.root_group = main_group
display.refresh()
while True:
    pass
