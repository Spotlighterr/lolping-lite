# lolPing Lite

Bản làm lại siêu nhẹ cho Windows của **lolPing** — thả vòng Ping & Emote kiểu Liên Minh Huyền Thoại ngay trên màn hình desktop, chỉ tốn khoảng **40–50 MB RAM** thay vì ~600 MB của bản Electron.

Mình viết bản này bằng Go thuần Win32 vì máy 16 GB RAM mở lắm tab Chrome + VS Code quá, muốn giữ lại con app ping vui tay mà không bị ngốn RAM hay tụt FPS.

## Cách dùng

Chạy file `lolPingLite.exe`, app sẽ nằm gọn dưới khay hệ thống (System Tray):

- **Giữ `Alt` (hoặc `G`) + Chuột trái rồi kéo:** Mở vòng Ping 8 hướng, thả chuột để ping kèm âm thanh.
- **Giữ `Ctrl` + Chuột trái rồi kéo:** Mở vòng Emote.
- **Chuột phải khi đang kéo:** Hủy (`BACK`).
- **Chuột phải icon dưới khay hệ thống:** Bật/tắt nhanh, test ping hoặc thoát app.

*(Bản này chỉ chạy local trên máy cho gọn, không có tính năng phòng multiplayer.)*

## Tự build từ mã nguồn

Cần cài sẵn **Go 1.24+** trên Windows:

```powershell
go build -ldflags="-H windowsgui -s -w" -o lolPingLite.exe .
```

> Nhớ để thư mục `assets/emotes` cạnh `lolPingLite.exe` nếu muốn emote chạy được animation.

## Credit

- Ý tưởng, thiết kế vòng ping, animation và toàn bộ asset gốc đều từ dự án **[darrenprx/lolPing](https://github.com/darrenprx/lolPing)** của **Danrui Meng (`darrenprx`)** — cảm ơn tác giả rất nhiều vì một món đồ chơi desktop cực kỳ vui!
- Hình ảnh và âm thanh *League of Legends* thuộc bản quyền của **Riot Games, Inc.** Dự án này chỉ là bản fan-made phi thương mại.
