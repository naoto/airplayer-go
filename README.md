AirPlayer
================================================================================

Apple TV などの AirPlay 受信機で動画を再生するコマンドラインツールです。

- ローカルの動画ファイル / ディレクトリ / URL / YouTube を再生
- リピート再生・シャッフル再生
- AirPlay 2（tvOS）の HomeKit ペアリングと暗号化通信に対応
- Go の単一バイナリ。avahi や Bonjour SDK などの外部ライブラリは不要


目次
--------------------------------------------------------------------------------

- [動作環境](#動作環境)
- [インストール](#インストール)
- [クイックスタート](#クイックスタート)
- [コマンド](#コマンド)
- [使い方](#使い方)
- [ペアリングと認証](#ペアリングと認証)
- [対応フォーマット](#対応フォーマット)
- [トラブルシューティング](#トラブルシューティング)
- [仕組み](#仕組み)
- [開発](#開発)


動作環境
--------------------------------------------------------------------------------

- macOS または Linux
- Go 1.17 以上（ビルドする場合）
- AirPlay 受信機
  - Apple TV（第 2 世代以降）
  - AirPlay 2 対応の受信機（tvOS の Apple TV、macOS の AirPlay レシーバーなど）
- YouTube を再生する場合は [youtube-dl](https://github.com/ytdl-org/youtube-dl) か [yt-dlp](https://github.com/yt-dlp/yt-dlp)
- 再生するマシンと受信機が同じネットワーク（同じサブネット）にあること


インストール
--------------------------------------------------------------------------------

### go install

```sh
$ go install github.com/naoto/airplayer-go/cmd/airplayer@latest
```

`$(go env GOPATH)/bin` に `airplayer` がインストールされます。PATH に含まれていることを確認してください。

### ソースからビルド

```sh
$ git clone https://github.com/naoto/airplayer-go.git
$ cd airplayer-go
$ go build ./cmd/airplayer
$ ./airplayer version
```


クイックスタート
--------------------------------------------------------------------------------

```sh
# 1. ネットワーク上の AirPlay 受信機を確認する
$ airplayer devices
0: Living Room (Model: AppleTV11,1, Resolution: unknown, Version: 17A123, IP: 192.168.1.20:7000)

# 2. 最近の Apple TV では初回だけペアリングする（画面に PIN が表示されます）
$ airplayer pair
Enter the PIN shown on Living Room: 1234
Paired with Living Room.

# 3. 再生する
$ airplayer play ~/Movies/AKIRA.mp4
```


コマンド
--------------------------------------------------------------------------------

```
airplayer play <URI|PATH>... [-r|--repeat] [-s|--shuffle] [-d|--device=N]
airplayer pair [-d|--device=N]
airplayer devices
airplayer version
airplayer help
```

### `play`

動画を再生します。引数には次のものを指定でき、複数並べると順番に再生します。

| 引数 | 説明 |
| --- | --- |
| ファイルのパス | 対応している拡張子の動画ファイル。`~/` で始まるパスも展開されます |
| ディレクトリのパス | 直下にある対応動画を名前順にすべて再生します（サブディレクトリは対象外） |
| URL | `http://` / `https://` の動画 URL |
| YouTube の URL | `youtube.com` / `youtu.be` の URL。youtube-dl（または yt-dlp）で実際の動画 URL を取得します |

| オプション | 説明 |
| --- | --- |
| `-r`, `--repeat` | プレイリストを繰り返し再生します（Ctrl-C で終了） |
| `-s`, `--shuffle` | 周回ごとに順番をシャッフルします |
| `-d N`, `--device=N` | `devices` で表示される番号 N の受信機で再生します（既定は 0） |

オプションは引数の前後どちらに書いてもかまいません。

### `pair`

受信機とペアリングし、認証情報を保存します。詳しくは「[ペアリングと認証](#ペアリングと認証)」を参照してください。

| オプション | 説明 |
| --- | --- |
| `-d N`, `--device=N` | ペアリングする受信機の番号（既定は 0） |

### `devices`

ネットワーク上の AirPlay 受信機を一覧表示します（探索に約 2 秒かかります）。
先頭の番号を `play` / `pair` の `--device` に指定します。

### `version`

バージョンを表示します（`--version` も可）。

### 終了コード

| コード | 意味 |
| --- | --- |
| 0 | 正常終了 |
| 1 | エラー（受信機が見つからない、再生に失敗した、など） |
| 2 | コマンドの使い方が間違っている |
| 130 | Ctrl-C で中断した |


使い方
--------------------------------------------------------------------------------

### ローカルの動画ファイルを再生する

```sh
$ airplayer play '~/Movies/Trailers/007 SKYFALL.mp4'

 Source: http://192.168.1.10:7070/007%20SKYFALL.mp4
  Title: 007 SKYFALL.mp4
 Device: Living Room (Resolution: unknown)
   Time: 00:00:20 |=====                                         | 11% Streaming
```

ローカルファイルを再生するときは、airplayer が一時的に HTTP サーバー（ポート 7070、使用中なら空きポート）を立てます。
受信機はそこから動画を取得します。再生中はターミナルを閉じないでください。

### ディレクトリ内の動画をすべて再生する

```sh
$ airplayer play ~/Movies/Trailers

 Source: http://192.168.1.10:7070/007%20Casino%20Royale.mp4
  Title: 007 Casino Royale.mp4
 Device: Living Room (Resolution: unknown)
   Time: 00:02:33 |==============================================| 100% Complete

 Source: http://192.168.1.10:7070/007%20Quantum%20Of%20Solace.mp4
  Title: 007 Quantum Of Solace.mp4
 Device: Living Room (Resolution: unknown)
   Time: 00:02:01 |==============================================| 100% Complete
```

### ネット上の動画を再生する

```sh
$ airplayer play http://heinlein.local/Movies/AKIRA.m4v

 Source: http://heinlein.local/Movies/AKIRA.m4v
  Title: AKIRA.m4v
 Device: Living Room (Resolution: unknown)
   Time: 00:04:25 |=                                              | 3% Streaming
```

URL の場合は受信機が直接その URL から動画を取得します。受信機からアクセスできる URL を指定してください。

### YouTube の動画を再生する

```sh
$ airplayer play 'https://www.youtube.com/watch?v=QH2-TGUlwu4'
```

`youtube-dl` か `yt-dlp` が PATH 上に必要です（`youtube-dl` を優先して使います）。

### 複数のファイルをまとめて再生する

```sh
$ airplayer play intro.mp4 ~/Movies/Trailers https://example.com/outro.mp4
```

### リピート再生

```sh
# 1 本をリピート
$ airplayer play '~/Movies/Trailers/007 SKYFALL.mp4' --repeat

# ディレクトリ全体をリピート
$ airplayer play ~/Movies/Trailers -r
```

### シャッフル再生

```sh
$ airplayer play ~/Movies/Trailers --shuffle

# シャッフルしながら延々と再生
$ airplayer play ~/Movies/Trailers -s -r
```

### 受信機を選ぶ

受信機が複数ある場合は、`devices` で番号を確認して `--device` で指定します。

```sh
$ airplayer devices
0: John's Apple TV (Model: AppleTV5,3, Resolution: 1280x720, Version: 11A502, IP: 10.0.1.2:7000)
1: Jane's Apple TV (Model: AppleTV6,2, Resolution: 1920x1080, Version: 11A502, IP: 10.0.1.3:7000)

$ airplayer play --device 1 '~/Movies/GHOST IN THE SHELL.mp4'
```

番号は受信機名の順に振られます。存在しない番号を指定した場合は 0 番の受信機を使います。

### 再生を止める

Ctrl-C を押すと、受信機の再生を止めてから終了します。


ペアリングと認証
--------------------------------------------------------------------------------

最近の Apple TV（tvOS）や AirPlay 2 対応の受信機では、再生の前に認証が必要です。
airplayer は接続時に次の順で認証を試みます。

1. **保存済みの認証情報**（`airplayer pair` で作成したもの）で pair-verify を行い、暗号化通信で再生
2. 認証情報がなければ **transient pairing**（PIN 入力不要の一時ペアリング）を試す
3. どちらも使えなければ、古い Apple TV 向けの**認証なしの通信**で再生

3 で受信機に拒否された場合は、次のように表示されます。

```
airplay: device requires pairing (POST /play: HTTP 403)
Run `airplayer pair -d 0` first.
```

### ペアリングする

```sh
$ airplayer pair
Enter the PIN shown on Living Room: 1234
Paired with Living Room.
```

コマンドを実行するとテレビに 4 桁の PIN が表示されるので、それを入力します。ペアリングは受信機ごとに 1 回だけで済みます。
受信機側には `airplayer@<ホスト名>` という名前で登録されます。

### 認証情報の保存場所

| OS | パス |
| --- | --- |
| macOS | `~/Library/Application Support/airplayer/credentials.json` |
| Linux | `~/.config/airplayer/credentials.json`（`$XDG_CONFIG_HOME` があればその下） |

ファイルには秘密鍵が含まれるため、パーミッション `0600` で保存します。
ペアリングをやり直したいときは、このファイルを削除してから `airplayer pair` を実行してください。


対応フォーマット
--------------------------------------------------------------------------------

[AirPlay Overview - Preparing Your Media for AirPlay](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/AirPlayGuide/PreparingYourMediaforAirPlay/PreparingYourMediaforAirPlay.html)

| 拡張子 | MIME タイプ |
| --- | --- |
| `.ts` | video/MP2T |
| `.mov` | video/quicktime |
| `.m4v` | video/mp4 |
| `.mp4` | video/mp4 |

拡張子の大文字・小文字は区別しません。上記以外の拡張子は `unsupported media` エラーになります。
コンテナが対応していても、中身のコーデック（H.264 / HEVC など）が受信機で再生できないことがあります。


トラブルシューティング
--------------------------------------------------------------------------------

### `AirPlay devices not found.` と表示される

- 受信機の電源が入っていて、同じネットワークに接続されているか確認してください。
- mDNS（UDP 5353 番、マルチキャスト `224.0.0.251`）がファイアウォールやルーターで遮断されていないか確認してください。
- VPN 接続中や、ゲスト Wi-Fi など端末間通信が禁止されたネットワークでは見つからないことがあります。

### `device requires pairing` と表示される

`airplayer pair -d <番号>` でペアリングしてください。

### Mac の AirPlay レシーバーに接続できない（HTTP 403）

Mac のシステム設定 → 一般 → AirDrop と Handoff → 「AirPlay を許可」が「現在のユーザ」になっていると、
他のクライアントからの接続は拒否されます。「同じネットワーク上のすべてのユーザ」か「全員」に変更してください。

### `pair-verify ... (pair again with airplayer pair)` と表示される

受信機側でペアリングが解除されたか、受信機がリセットされた可能性があります。
`credentials.json` を削除してから、もう一度 `airplayer pair` を実行してください。

### `authentication failed (wrong PIN?)` と表示される

PIN が間違っています。もう一度 `airplayer pair` を実行してください。
`too many attempts` と表示された場合は、しばらく待ってからやり直してください。

### ローカルファイルが再生されない / `playback did not start`

- 受信機から再生するマシンのポート 7070（または表示された `Source:` のポート）に接続できるか確認してください。
  macOS のファイアウォールで `airplayer` の受信接続が許可されている必要があります。
- 動画のコーデックが受信機で再生できる形式か確認してください。


仕組み
--------------------------------------------------------------------------------

- **探索**: mDNS で `_airplay._tcp.local` を問い合わせ、受信機の名前・アドレス・TXT レコードを取得します。
- **認証**: HomeKit 方式のペアリング（SRP-6a 3072bit / SHA-512、Ed25519、X25519）を行い、
  以降の HTTP 通信を ChaCha20-Poly1305 で暗号化します。
- **再生**: 1 本の持続的な接続で `POST /play`（binary plist）を送り、`GET /playback-info` を 1 秒ごとに取得して進み具合を表示します。
  受信機は `/play` を送った接続が切れると再生を止めるため、再生中は接続を保ちます。

```
.
├── cmd/airplayer/       # CLI
│   ├── main.go          # コマンド（play / pair / devices / version）
│   ├── controller.go    # 再生の制御、ローカルファイル配信用の HTTP サーバー
│   ├── playlist.go      # プレイリスト（リピート / シャッフル）
│   ├── media.go         # ファイル・URL・YouTube の判定と解決
│   ├── progressbar.go   # 進捗バー表示
│   └── credentials.go   # 認証情報の保存
└── airplay/             # AirPlay プロトコルの実装
    ├── discovery.go # mDNS による探索
    ├── client.go    # 接続・再生・状態取得
    ├── pairing.go   # pair-setup / pair-verify / transient pairing
    ├── srp.go       # SRP-6a
    ├── conn.go      # HTTP over 暗号化コネクション
    ├── plist.go     # binary / XML plist
    └── tlv8.go      # TLV8
```


開発
--------------------------------------------------------------------------------

```sh
$ go vet ./...
$ go test ./...
```

`airplay` パッケージのテストでは、偽の AirPlay 受信機を立てて、ペアリング・暗号化通信・再生要求を一通り検証します。
実機が不要なので、そのまま実行できます。


LICENSE
--------------------------------------------------------------------------------

&copy; 2012 - 2013 Tomohiro TAIRA.
This project is licensed under the MIT license.
See LICENSE for details.
