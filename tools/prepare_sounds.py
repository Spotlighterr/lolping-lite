"""Build-time OGG -> PCM using an installed FFmpeg shared library (no runtime DLL)."""
import ctypes as c
import pathlib
import struct
import wave
import sys
from prepare_emotes import ROOT, SLUGS

lib = c.CDLL(sys.argv[1] if len(sys.argv) > 1 else r'C:\Program Files\XnViewMP\ffmpeg-8.dll')
P = c.c_void_p
def fn(name, restype, *args):
    f = getattr(lib, name)
    f.restype, f.argtypes = restype, args
    return f

open_input = fn('avformat_open_input', c.c_int, c.POINTER(P), c.c_char_p, P, P)
info = fn('avformat_find_stream_info', c.c_int, P, P)
best = fn('av_find_best_stream', c.c_int, P, c.c_int, c.c_int, c.c_int, c.POINTER(P), c.c_int)
alloc = fn('avcodec_alloc_context3', P, P)
params = fn('avcodec_parameters_to_context', c.c_int, P, P)
codec_open = fn('avcodec_open2', c.c_int, P, P, P)
packet_alloc = fn('av_packet_alloc', P)
frame_alloc = fn('av_frame_alloc', P)
read = fn('av_read_frame', c.c_int, P, P)
send = fn('avcodec_send_packet', c.c_int, P, P)
receive = fn('avcodec_receive_frame', c.c_int, P, P)
unref = fn('av_packet_unref', None, P)
free_frame = fn('av_frame_free', None, c.POINTER(P))
free_packet = fn('av_packet_free', None, c.POINTER(P))
free_ctx = fn('avcodec_free_context', None, c.POINTER(P))
close_input = fn('avformat_close_input', None, c.POINTER(P))

for slug in SLUGS:
    path = ROOT / '.upstream/assets/emotes' / (slug + '.ogg')
    raw = path.read_bytes()
    ident = raw.index(b'OpusHead')
    channels = raw[ident+9]
    rate = 48000
    fmt, decoder = P(), P()
    assert open_input(c.byref(fmt), str(path).encode(), None, None) == 0
    assert info(fmt, None) >= 0
    stream_idx = best(fmt, 1, -1, -1, c.byref(decoder), 0)
    assert stream_idx >= 0
    streams = P.from_address(fmt.value+48).value
    stream = P.from_address(streams+stream_idx*8).value
    codecpar = P.from_address(stream+16).value
    ctx = P(alloc(decoder))
    assert params(ctx, codecpar) >= 0
    assert codec_open(ctx, decoder, None) >= 0
    pkt, frame = P(packet_alloc()), P(frame_alloc())
    pcm = bytearray()
    while read(fmt, pkt) >= 0:
        # AVPacket.stream_index follows buf, pts, dts, data, size.
        if c.c_int.from_address(pkt.value+36).value == stream_idx:
            assert send(ctx, pkt) >= 0
            while receive(ctx, frame) >= 0:
                n = c.c_int.from_address(frame.value+112).value
                sample_fmt = c.c_int.from_address(frame.value+116).value
                assert sample_fmt == 8, sample_fmt  # AV_SAMPLE_FMT_FLTP
                planes = [c.cast(P.from_address(frame.value+8*k).value, c.POINTER(c.c_float)) for k in range(channels)]
                for i in range(n):
                    for plane in planes:
                        pcm.extend(struct.pack('<h', max(-32768, min(32767, round(plane[i]*32768)))))
        unref(pkt)
    with wave.open(str(ROOT / 'assets/emotes' / (slug + '.wav')), 'wb') as out:
        out.setparams((channels, 2, rate, 0, 'NONE', 'not compressed'))
        out.writeframes(pcm)
    free_frame(c.byref(frame)); free_packet(c.byref(pkt))
    free_ctx(c.byref(ctx)); close_input(c.byref(fmt))
    print(slug, channels, rate, len(pcm))
