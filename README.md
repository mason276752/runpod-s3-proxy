# runpod_proxy

一個本地 HTTP Proxy，用於修復 RunPod S3 API 的相容性問題，使各種標準 S3 工具能夠正常存取 RunPod 儲存空間。

## 問題背景

RunPod 的 S3 API 存在若干不符合標準的行為，導致大多數 S3 瀏覽工具與客戶端無法正常使用：

- 日期標頭使用 `UTC`，而非符合 RFC 規範的 `GMT`
- 回應包含非標準標頭，導致 S3 客戶端解析錯誤

因此，能夠正常連接 AWS S3 或其他 S3 相容服務的工具，直接指向 RunPod 時往往會失敗或行為異常。

## 解決方式

此 Proxy 介於 S3 工具與 RunPod 之間，負責：

1. 接受來自任意 S3 客戶端的請求
2. 使用 RunPod 憑證以 AWS Signature Version 4 重新簽署請求
3. 修正回應標頭（例如將日期欄位的 `UTC` 替換為 `GMT`）後回傳給客戶端

將 S3 工具的 Endpoint 改指向 `http://localhost:9000` 即可正常使用。

## 設定

憑證從工作目錄的 `.env` 檔案讀取，也可直接設定為環境變數。

複製 `.env.example` 為 `.env` 並填入資訊：

```
cp .env.example .env
```

`.env` 格式：

```
RUNPOD_ACCESS_KEY=user_xxxxxxxxxxxxxxxxxx
RUNPOD_SECRET_KEY=rps_xxxxxxxxxxxxxxxxxx
RUNPOD_REGION=eu-ro-1
```

- `RUNPOD_ACCESS_KEY` / `RUNPOD_SECRET_KEY`：可在 RunPod 控制台的 **Storage → API Keys** 取得
- `RUNPOD_REGION`：RunPod 儲存空間的地區代碼（例如 `eu-ro-1`），用於組出上游 API Endpoint

若環境變數已預先設定，`.env` 檔案可省略。

## 編譯與執行

```bash
go build -o runpod_proxy runpod_proxy.go
./runpod_proxy            # 監聽 :9000
./runpod_proxy :8080      # 自訂 Port
```

啟動後，將 S3 工具的 Endpoint 設為 `http://localhost:9000` 即可。
