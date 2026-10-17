# ============================================================================
# teste-curl.ps1 — smoke test do Jungle Desafio, so com curl.exe
# Requisito: `docker compose up -d` (api em :8081, keycloak em :8080)
# Uso:  powershell -ExecutionPolicy Bypass -File .\teste-curl.ps1
#
# NOTA DE WINDOWS: o PowerShell 5.1 REMOVE as aspas do JSON ao chamar um .exe
# nativo. Por isso todo corpo vai por ARQUIVO (--data-binary "@arquivo.json").
# Em cmd.exe, o equivalente e escapar: -d "{\"campo\":\"valor\"}".
# ============================================================================
$ErrorActionPreference = 'Stop'
$api = 'http://localhost:8081'
$idp = 'http://localhost:8080/realms/jungleg'
$pasta = Join-Path $env:TEMP ("jungleg-smoke-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $pasta | Out-Null
$resultados = New-Object System.Collections.ArrayList
$script:n = 0

function Token([string]$client, [string]$segredo) {
  $bruto = curl.exe -s -X POST "$idp/protocol/openid-connect/token" `
    --data-urlencode 'grant_type=client_credentials' `
    --data-urlencode "client_id=$client" `
    --data-urlencode "client_secret=$segredo"
  $tok = ($bruto -join '') | ConvertFrom-Json
  if (-not $tok.access_token) { throw "sem access_token para $client" }
  return $tok.access_token
}

function Chamar([string]$metodo, [string]$caminho, [string]$token, $corpo, [string]$chave) {
  $saida = Join-Path $pasta 'saida.txt'
  $a = @('-s', '-o', $saida, '-w', '%{http_code}', '-X', $metodo, "$api$caminho")
  if ($token) { $a += @('-H', "Authorization: Bearer $token") }
  if ($chave) { $a += @('-H', "Idempotency-Key: $chave") }
  if ($null -ne $corpo) {
    $script:n++
    $arq = Join-Path $pasta ("corpo-" + $script:n + ".json")
    [System.IO.File]::WriteAllText($arq, $corpo, (New-Object System.Text.UTF8Encoding($false)))
    $a += @('-H', 'Content-Type: application/json', '--data-binary', "@$arq")
  }
  $codigo = (& curl.exe @a) -join ''
  $texto = ''
  if (Test-Path $saida) { $texto = Get-Content $saida -Raw }
  $status = 0; [void][int]::TryParse($codigo, [ref]$status)
  return [pscustomobject]@{ Status = $status; Corpo = $texto }
}

function Passo([string]$nome, [int]$esperado, $resposta, [string]$contem) {
  $ok = ($resposta.Status -eq $esperado)
  if ($ok -and $contem -and ($resposta.Corpo -notlike "*$contem*")) { $ok = $false }
  $rotulo = if ($ok) { 'OK    ' } else { 'FALHOU' }
  $corpo = $resposta.Corpo
  if ($corpo.Length -gt 170) { $corpo = $corpo.Substring(0, 170) + '...' }
  [void]$resultados.Add([pscustomobject]@{ Passo = $nome; Esperado = $esperado; Recebido = $resposta.Status; Resultado = $rotulo })
  Write-Host ("[{0}] {1} (esperado {2}, veio {3})" -f $rotulo, $nome, $esperado, $resposta.Status)
  Write-Host ("        {0}" -f $corpo)
}

try {
  $interno   = Token 'internal-service'   'internal-service-secret'
  $provedorA = Token 'provider-service'   'provider-service-secret'
  $provedorB = Token 'provider-b-service' 'provider-b-service-secret'
  $sufixo    = [guid]::NewGuid().ToString('N').Substring(0, 12)
  $player    = [guid]::NewGuid().ToString()

  Write-Host "`n=== PUBLICOS ==="
  Passo 'GET /health/live'  200 (Chamar 'GET' '/health/live'  $null $null $null) '"status":"UP"'
  Passo 'GET /health/ready' 200 (Chamar 'GET' '/health/ready' $null $null $null) '"workers":"UP"'
  Passo 'GET /metrics'      200 (Chamar 'GET' '/metrics'      $null $null $null) 'jungleg_wager_outbox_pending_age_seconds'

  Write-Host "`n=== AUTENTICACAO ==="
  Passo 'GET /wallets sem token' 401 (Chamar 'GET' "/wallets/$player" $null $null $null)

  Write-Host "`n=== CARTEIRA ==="
  $abertura = Chamar 'POST' '/wallets' $interno (@{ playerId = $player; initialBalance = @{ amount = '1000.00'; currency = 'BRL' } } | ConvertTo-Json -Compress) $null
  Passo 'POST /wallets 1000.00' 201 $abertura '"amount":"1000.00"'
  $wallet = ($abertura.Corpo | ConvertFrom-Json).id
  Passo 'GET /wallets/{id}' 200 (Chamar 'GET' "/wallets/$wallet" $interno $null $null) '"version":1'

  Write-Host "`n=== OPERACOES ==="
  $betBody = @{ providerId='provider-a'; externalTransactionId="bet-$sufixo"; playerId=$player; walletId=$wallet; roundId="r-$sufixo"; gameId='fortune-chimp'; kind='BET'; money=@{ amount='25.00'; currency='BRL' } } | ConvertTo-Json -Compress
  $bet = Chamar 'POST' '/wagering/transactions' $provedorA $betBody "provider-a:bet-$sufixo"
  Passo 'BET 25.00' 201 $bet '"status":"PROCESSED"'
  $txID = ($bet.Corpo | ConvertFrom-Json).transactionId
  Passo 'BET replay (mesma chave)' 200 (Chamar 'POST' '/wagering/transactions' $provedorA $betBody "provider-a:bet-$sufixo") '"idempotentReplay":true'
  $betDiferente = @{ providerId='provider-a'; externalTransactionId="bet-$sufixo"; playerId=$player; walletId=$wallet; roundId="r-$sufixo"; gameId='fortune-chimp'; kind='BET'; money=@{ amount='30.00'; currency='BRL' } } | ConvertTo-Json -Compress
  Passo 'BET mesma chave, corpo diferente' 409 (Chamar 'POST' '/wagering/transactions' $provedorA $betDiferente "provider-a:bet-$sufixo") 'IDEMPOTENCY_CONFLICT'
  Passo 'BET sem Idempotency-Key' 400 (Chamar 'POST' '/wagering/transactions' $provedorA $betBody $null) 'IDEMPOTENCY_KEY_REQUIRED'
  $semSaldo = @{ providerId='provider-a'; externalTransactionId="bet-sem-saldo-$sufixo"; playerId=$player; walletId=$wallet; roundId="r-$sufixo"; gameId='fortune-chimp'; kind='BET'; money=@{ amount='5000.00'; currency='BRL' } } | ConvertTo-Json -Compress
  Passo 'BET 5000.00 sem saldo' 422 (Chamar 'POST' '/wagering/transactions' $provedorA $semSaldo "provider-a:bet-sem-saldo-$sufixo") 'INSUFFICIENT_FUNDS'
  $loss = @{ providerId='provider-a'; externalTransactionId="loss-$sufixo"; playerId=$player; walletId=$wallet; roundId="r-$sufixo"; gameId='fortune-chimp'; kind='LOSS'; money=@{ amount='0.00'; currency='BRL' } } | ConvertTo-Json -Compress
  Passo 'LOSS 0.00' 201 (Chamar 'POST' '/wagering/transactions' $provedorA $loss "provider-a:loss-$sufixo") '"status":"PROCESSED"'
  $refund = @{ providerId='provider-a'; externalTransactionId="refund-$sufixo"; playerId=$player; walletId=$wallet; roundId="r-$sufixo"; gameId='fortune-chimp'; kind='REFUND'; money=@{ amount='25.00'; currency='BRL' }; referenceExternalTransactionId="bet-$sufixo" } | ConvertTo-Json -Compress
  Passo 'REFUND da BET 25.00' 201 (Chamar 'POST' '/wagering/transactions' $provedorA $refund "provider-a:refund-$sufixo") '"balance":{"amount":"1000.00"'
  $rollback = @{ providerId='provider-a'; externalTransactionId="rb-$sufixo"; playerId=$player; walletId=$wallet; roundId="r-$sufixo"; gameId='fortune-chimp'; kind='ROLLBACK'; money=@{ amount='25.00'; currency='BRL' }; referenceExternalTransactionId="bet-$sufixo" } | ConvertTo-Json -Compress
  Passo 'ROLLBACK da BET ja estornada' 422 (Chamar 'POST' '/wagering/transactions' $provedorA $rollback "provider-a:rb-$sufixo") 'ALREADY_REVERSED'

  Write-Host "`n=== DEFEITO 50: REFUND DEPOIS DE ROLLBACK ==="
  $player2 = [guid]::NewGuid().ToString()
  $ab2 = Chamar 'POST' '/wallets' $interno (@{ playerId=$player2; initialBalance=@{ amount='100.00'; currency='BRL' } } | ConvertTo-Json -Compress) $null
  $w2 = ($ab2.Corpo | ConvertFrom-Json).id
  [void](Chamar 'POST' '/wagering/transactions' $provedorA (@{ providerId='provider-a'; externalTransactionId="b2-$sufixo"; playerId=$player2; walletId=$w2; roundId="r2-$sufixo"; gameId='g'; kind='BET'; money=@{ amount='40.00'; currency='BRL' } } | ConvertTo-Json -Compress) "provider-a:b2-$sufixo")
  Passo 'ROLLBACK da BET' 201 (Chamar 'POST' '/wagering/transactions' $provedorA (@{ providerId='provider-a'; externalTransactionId="r2-$sufixo"; playerId=$player2; walletId=$w2; roundId="r2-$sufixo"; gameId='g'; kind='ROLLBACK'; money=@{ amount='40.00'; currency='BRL' }; referenceExternalTransactionId="b2-$sufixo" } | ConvertTo-Json -Compress) "provider-a:r2-$sufixo") '"amount":"100.00"'
  Passo 'REFUND depois do ROLLBACK' 422 (Chamar 'POST' '/wagering/transactions' $provedorA (@{ providerId='provider-a'; externalTransactionId="f2-$sufixo"; playerId=$player2; walletId=$w2; roundId="r2-$sufixo"; gameId='g'; kind='REFUND'; money=@{ amount='40.00'; currency='BRL' }; referenceExternalTransactionId="b2-$sufixo" } | ConvertTo-Json -Compress) "provider-a:f2-$sufixo") 'ALREADY_REVERSED'
  Passo 'GET carteira2 (saldo intacto)' 200 (Chamar 'GET' "/wallets/$w2" $interno $null $null) '"amount":"100.00"'

  Write-Host "`n=== REFERENCIA PENDENTE ==="
  $pendente = @{ providerId='provider-a'; externalTransactionId="pend-$sufixo"; playerId=$player; walletId=$wallet; roundId="r-$sufixo"; gameId='g'; kind='REFUND'; money=@{ amount='25.00'; currency='BRL' }; referenceExternalTransactionId="nao-existe-$sufixo" } | ConvertTo-Json -Compress
  Passo 'REFUND de referencia inexistente' 202 (Chamar 'POST' '/wagering/transactions' $provedorA $pendente "provider-a:pend-$sufixo") 'PENDING_REFERENCE'

  Write-Host "`n=== LEITURAS ==="
  Passo 'GET ledger?limit=50' 200 (Chamar 'GET' "/wallets/$wallet/ledger?limit=50" $interno $null $null) '"nextCursor":null'
  Passo 'GET ledger limit invalido' 400 (Chamar 'GET' "/wallets/$wallet/ledger?limit=abc" $interno $null $null) 'INVALID_REQUEST'
  Passo 'POST reconciliacao' 200 (Chamar 'POST' "/wallets/$wallet/reconciliation" $interno $null $null) '"consistent":true'
  Passo 'GET por identidade externa (provider-a)' 200 (Chamar 'GET' "/providers/provider-a/wagering/transactions/bet-$sufixo" $provedorA $null $null) '"status":"PROCESSED"'
  Passo 'GET mesma transacao pelo provider-b' 404 (Chamar 'GET' "/providers/provider-b/wagering/transactions/bet-$sufixo" $provedorB $null $null) 'NOT_FOUND'
  Passo 'GET /wagering/transactions/{id} (interno)' 200 (Chamar 'GET' "/wagering/transactions/$txID" $interno $null $null) '"kind":"BET"'
  Passo 'GET transacao interna com token de provedor' 403 (Chamar 'GET' "/wagering/transactions/$txID" $provedorA $null $null) 'FORBIDDEN'

  Write-Host "`n=== METRICA APOS AS OPERACOES ==="
  Passo 'GET /metrics com a serie de BET' 200 (Chamar 'GET' '/metrics' $null $null $null) 'jungleg_wager_operations_total{kind="BET",status="PROCESSED"}'
}
finally {
  Remove-Item -Recurse -Force $pasta -ErrorAction SilentlyContinue
}

Write-Host "`n=== RESUMO ==="
$resultados | Format-Table -AutoSize
$falhas = @($resultados | Where-Object { $_.Resultado -ne 'OK    ' }).Count
Write-Host ("Passos: {0}   Falhas: {1}" -f $resultados.Count, $falhas)
if ($falhas -gt 0) { exit 1 } else { exit 0 }
