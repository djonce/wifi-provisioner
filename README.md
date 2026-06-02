# wifi-provisioner

无头 Linux 设备的 **Wi-Fi 配网工具**：开机若未联网，自动开启一个配置热点 + 强制门户（captive portal）网页；用手机连上热点，在弹出的网页里选网络、填密码，设备即连入目标 Wi-Fi 并关闭热点。

为 **Radxa Cubie A7Z**（Allwinner A733，ARM64，板载 Wi-Fi 6）的无界面 Debian 11 设计，但不绑定具体板子，任何带可做 AP 的 Wi-Fi 网卡的 Debian/Ubuntu 设备都能用。

---

## 工作原理

单个静态 Go 二进制，作为常驻 systemd 服务运行，状态机如下：

1. **检测** —— 开机后判断是否已联网（向 `generate_204` 探测 + 兜底 TCP 拨测）。已联网则待命，不打扰正常使用。
2. **起热点** —— 未联网时把网卡切到 AP 模式，广播 `CubieSetup-XXXX`（XXXX 取自网卡 MAC 后两字节）。
3. **内置 DHCP + DNS 劫持** —— 纯 Go 实现，给手机分配地址、把所有域名解析指向本机，触发手机「登录网络」自动弹窗。**不依赖 dnsmasq**。
4. **配网网页** —— 内嵌于二进制，手机端自适应；列出周边 Wi-Fi、选网络填密码、实时显示连接进度。
5. **连接（单射频时序）** —— 提交后先关 AP/门户，切回 station 模式连目标网络并校验出网。
6. **收尾** —— 成功则关热点、上报成功；失败则热点重新出现，手机重连即可重试。
7. **重新配网** —— 待命期间，可用哨兵文件或 GPIO 按键随时重新进入配网（见下）。

后端**自动适配**：系统跑 NetworkManager 时走 `nmcli`；否则回退到 `hostapd + wpa_supplicant`。同一个二进制两种环境都能用。

---

## 前置条件（一条命令验证 AP 模式）

最关键的硬性前提：**网卡驱动必须支持 AP 模式**。在板子上执行：

```bash
iw list | sed -n '/Supported interface modes/,/Band/p'
```

输出里必须出现 `* AP`。Cubie A7Z 的 Wi-Fi 6 模组多为 AIC8800 系列，若看不到 `AP`，需要更新/更换支持 AP 的驱动，否则本工具无法开热点。

运行时依赖（安装脚本会自动检查并提示缺啥）：

- **NetworkManager 环境**：只需 `nmcli`（系统自带），无需其它。
- **裸 wpa_supplicant 环境**：需要 `hostapd`、`wpa_supplicant`、`iw`，以及一个 DHCP 客户端（`udhcpc` 或 `isc-dhcp-client`）。
  ```bash
  sudo apt install hostapd wpa_supplicant iw udhcpc
  ```
- **可选**：GPIO 按键触发需要 `gpiod`（提供 `gpioget`/`gpioinfo`）。

---

## 安装

仓库已附带预编译的 ARM64 静态二进制（`bin/wifi-provisioner`，无任何动态库依赖）。把整个目录拷到板子上，然后：

```bash
sudo ./install.sh
sudo systemctl start wifi-provisioner
journalctl -u wifi-provisioner -f   # 看日志
```

`install.sh` 会：装二进制到 `/usr/local/bin`、写默认配置到 `/etc/wifi-provisioner/config.json`、安装并 `enable` systemd 服务（开机自启）、检查依赖。

### 自行编译

需要 Go 1.21+：

```bash
make build           # 产出 bin/wifi-provisioner（CGO_ENABLED=0 静态 arm64）
make test            # 单元测试
```

在 x86 开发机上交叉编译给板子用，同样 `make build` 即可（已固定 `GOOS=linux GOARCH=arm64`）。

---

## 使用流程（终端用户视角）

1. 设备开机，没连过的网络环境 → 几秒后出现 Wi-Fi 热点 `CubieSetup-XXXX`。
2. 手机连接该热点（默认开放，无密码）。
3. 手机一般会自动弹出配网页；若没弹，浏览器打开 `http://192.168.4.1`。
4. 选择目标 Wi-Fi，填密码，点「连接」。
5. 热点会短暂消失。连接成功后设备即联网；**若热点重新出现，说明失败，重连热点再试**。

---

## 重新配网

设备已联网、热点已关闭后，两种方式可让它重新开热点配网：

- **哨兵文件**（开箱即用）：
  ```bash
  touch /var/lib/wifi-provisioner/reconfigure
  ```
  程序检测到该文件后会删除它并重新进入配网。

- **GPIO 按键**（可选）：先装 `gpiod`，用 `gpiodetect` / `gpioinfo` 找到按键所在的 chip 和 line，填进配置：
  ```json
  { "gpio_chip": "gpiochip0", "gpio_line": 12, "gpio_active_low": true, "gpio_hold_sec": 3 }
  ```
  长按设定秒数即触发。

---

## 配置参考

配置文件 `/etc/wifi-provisioner/config.json`（参见 `config.example.json`）。所有字段都有默认值，留空即用默认。

| 字段 | 默认 | 说明 |
|---|---|---|
| `iface` | 自动检测 | Wi-Fi 网卡名，留空自动找第一个无线网卡 |
| `ap_ssid` | `CubieSetup-XXXX` | 配网热点名，留空按 MAC 生成 |
| `ap_password` | 空 | 热点密码，留空=开放热点（≥8 位才生效 WPA2） |
| `ap_address` | `192.168.4.1` | 热点网关/门户 IP |
| `ap_prefix` | `24` | 子网前缀长度 |
| `dhcp_start` / `dhcp_end` | `.50` / `.150` | DHCP 地址池 |
| `lease_minutes` | `10` | 租约时长 |
| `web_port` | `80` | 门户网页端口 |
| `provision_timeout_min` | `0` | 配网超时分钟数，0=不超时 |
| `connect_timeout_sec` | `45` | 连接目标网络的超时 |
| `online_timeout_sec` | `5` | 单次联网探测超时 |
| `check_interval_sec` | `30` | 待命时多久复查一次连通性 |
| `connectivity_urls` | 三个 generate_204 | 联网探测地址（含国内可达项） |
| `sentinel_file` | `/var/lib/.../reconfigure` | 重新配网哨兵文件 |
| `gpio_*` | 关闭 | GPIO 按键触发，见上 |
| `debug` | `false` | 调试日志（也可用 `--debug`） |

改完配置 `sudo systemctl restart wifi-provisioner` 生效。

---

## 故障排查

- **不开热点 / hostapd 立刻退出** → 多半是驱动不支持 AP，先跑上面的 `iw list` 验证；`journalctl -u wifi-provisioner -f` 看详细错误（开 `debug` 更详细）。
- **手机连上热点但不弹配网页** → 直接浏览器访问 `http://192.168.4.1`。若 DNS 劫持因 53 端口被 `systemd-resolved` 占用而禁用，自动弹窗可能失效，但手动访问 IP 始终可用。
- **连接目标网络失败** → 多为密码错误；热点会自动重新出现，重连重试。裸 wpa_supplicant 环境还需确认装了 DHCP 客户端。
- **配网成功但重启后又开热点** → NetworkManager 环境会自动记住网络；裸 wpa_supplicant 环境需确保 `wpa_supplicant@<iface>` 等开机自连机制已启用。

---

## 安全说明

- 配网热点默认**开放无密码**，便于首次接入；处于不可信环境时建议在配置里设 `ap_password`（≥8 位）。
- 门户网页只在热点子网内（`SO_BINDTODEVICE` 绑定网卡）提供服务，不会暴露到其它网络。
- 服务以 root 运行（管理网卡、绑定 53/67/80 端口所必需）。

---

## 项目结构

```
cmd/wifi-provisioner/main.go     入口、参数、信号处理
internal/config/                 配置加载与归一化
internal/detect/                 联网检测
internal/backend/                后端抽象 + NetworkManager / 裸 wpa 两种实现
internal/portal/                 内置 DHCP、DNS 劫持、门户网页（含内嵌 HTML）
internal/trigger/                哨兵文件 / GPIO 触发
internal/app/                    状态机：把以上编排起来
```
