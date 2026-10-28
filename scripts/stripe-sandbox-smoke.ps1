<#
.SYNOPSIS
Runs end-to-end Stripe sandbox payment smoke tests.

.DESCRIPTION
The script creates disposable fixtures in the Docker test database and verifies:

1. Successful payment, booking confirmation, cancellation, and refund.
2. Declined payment, retryability, reuse of the PaymentIntent, and successful retry.
3. Room inventory before payment, after cancellation, and after retry.
4. Transactional-email outbox delivery through SMTP to Mailpit.

Ryoko and `stripe listen` must already be running with PAYMENT_PROVIDER=stripe.
The script refuses non-loopback API URLs unless -AllowRemoteApi is supplied.

.EXAMPLE
pwsh ./scripts/stripe-sandbox-smoke.ps1
#>

[CmdletBinding()]
param(
    [string]$BaseUrl = "http://127.0.0.1:8080",
    [string]$ComposeFile = (Join-Path $PSScriptRoot "..\docker_compose.test.yml"),
    [string]$PostgresService = "postgres-test",
    [string]$PostgresContainer = "",
    [string]$DatabaseUser = "test",
    [string]$DatabaseName = "testdb",
    [string]$MailpitBaseUrl = "http://127.0.0.1:8025",
    [int]$WebhookTimeoutSeconds = 30,
    [int]$RefundTimeoutSeconds = 90,
    [int]$EmailTimeoutSeconds = 30,
    [switch]$AllowRemoteApi
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
if (Test-Path variable:PSNativeCommandUseErrorActionPreference) {
    $PSNativeCommandUseErrorActionPreference = $false
}

function Write-Step {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Assert-True {
    param(
        [bool]$Condition,
        [string]$Message
    )
    if (-not $Condition) {
        throw $Message
    }
}

function Assert-Equal {
    param(
        [AllowNull()]$Actual,
        [AllowNull()]$Expected,
        [string]$Message
    )
    if ($Actual -ne $Expected) {
        throw "$Message (got '$Actual', want '$Expected')"
    }
}

function Invoke-NativeCapture {
    param(
        [string]$FilePath,
        [string[]]$Arguments
    )

    $output = (& $FilePath @Arguments 2>&1 | Out-String)
    return [PSCustomObject]@{
        ExitCode = $LASTEXITCODE
        Output   = $output
    }
}

function Invoke-RyokoJson {
    param(
        [string]$Method,
        [string]$Path,
        [hashtable]$Headers = @{},
        [AllowNull()]$Body = $null
    )

    $parameters = @{
        Method  = $Method
        Uri     = "$script:ApiBase$Path"
        Headers = $Headers
    }
    if ($null -ne $Body) {
        $parameters.ContentType = "application/json"
        $parameters.Body = $Body | ConvertTo-Json -Depth 8 -Compress
    }
    return Invoke-RestMethod @parameters
}

function Get-AuthHeaders {
    param([string]$AccessToken)
    return @{ Authorization = "Bearer $AccessToken" }
}

function New-SmokeUserSession {
    param([string]$Label)

    $stamp = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $email = "stripe-smoke-$Label-$stamp@example.com"
    $password = "correct horse battery staple"

    Invoke-RyokoJson -Method Post -Path "/auth/register" -Body @{
        email     = $email
        password  = $password
        full_name = "Stripe Smoke $Label"
    } | Out-Null

    $login = Invoke-RyokoJson -Method Post -Path "/auth/login" -Body @{
        email    = $email
        password = $password
    }
    Assert-True ([bool]$login.access_token) "Login did not return an access token"
    return [PSCustomObject]@{
        AccessToken = [string]$login.access_token
        Email       = $email
    }
}

function New-SmokeBooking {
    param(
        [long]$RoomTypeId,
        [string]$AccessToken,
        [datetime]$CheckIn,
        [datetime]$CheckOut,
        [string]$IdempotencyKey
    )

    $headers = Get-AuthHeaders $AccessToken
    $headers["Idempotency-Key"] = $IdempotencyKey
    $booking = Invoke-RyokoJson -Method Post -Path "/room-types/$RoomTypeId/bookings" -Headers $headers -Body @{
        check_in    = $CheckIn.ToString("yyyy-MM-dd")
        check_out   = $CheckOut.ToString("yyyy-MM-dd")
        rooms_count = 1
        guest_count = 2
    }
    Assert-Equal $booking.status "pending_payment" "New booking status is incorrect"
    return $booking
}

function New-PaymentSession {
    param(
        [long]$BookingId,
        [string]$AccessToken
    )

    $session = Invoke-RyokoJson -Method Post -Path "/me/bookings/$BookingId/payment-session" -Headers (Get-AuthHeaders $AccessToken)
    Assert-True ([bool]$session.payment_id) "Payment session has no payment ID"
    Assert-True ($session.provider_payment_id -like "pi_*") "Payment session is not backed by Stripe"
    Assert-True ([bool]$session.client_secret) "Stripe payment session has no client secret"
    Assert-Equal $session.currency "VND" "Payment currency is incorrect"
    return $session
}

function Get-Payment {
    param(
        [long]$BookingId,
        [string]$AccessToken
    )
    return Invoke-RyokoJson -Method Get -Path "/me/bookings/$BookingId/payment" -Headers (Get-AuthHeaders $AccessToken)
}

function Get-Booking {
    param(
        [long]$BookingId,
        [string]$AccessToken
    )
    return Invoke-RyokoJson -Method Get -Path "/me/bookings/$BookingId" -Headers (Get-AuthHeaders $AccessToken)
}

function Wait-ForPayment {
    param(
        [long]$BookingId,
        [string]$AccessToken,
        [scriptblock]$Condition,
        [int]$TimeoutSeconds,
        [string]$Description
    )

    $timer = [Diagnostics.Stopwatch]::StartNew()
    do {
        $payment = Get-Payment -BookingId $BookingId -AccessToken $AccessToken
        if (& $Condition $payment) {
            return $payment
        }
        Start-Sleep -Seconds 1
    } while ($timer.Elapsed.TotalSeconds -lt $TimeoutSeconds)

    throw "Timed out waiting for $Description; last payment status was '$($payment.status)'. Check stripe listen and STRIPE_WEBHOOK_SECRET."
}

function Confirm-StripePaymentIntent {
    param(
        [string]$PaymentIntentId,
        [string]$PaymentMethod
    )

    # Stripe can return exit code 0 for a declined card because the API request
    # itself succeeded. The authoritative result is the subsequent webhook.
    return Invoke-NativeCapture -FilePath "stripe" -Arguments @(
        "payment_intents", "confirm", $PaymentIntentId,
        "--payment-method", $PaymentMethod,
        "-d", "return_url=http://localhost:3000/payments/return",
        "-c"
    )
}

function Invoke-PsqlScalar {
    param([string]$Sql)

    $result = Invoke-NativeCapture -FilePath "docker" -Arguments @(
        "exec", $script:PostgresContainer,
        "psql", "-U", $DatabaseUser, "-d", $DatabaseName,
        "-At", "-v", "ON_ERROR_STOP=1", "-c", $Sql
    )
    if ($result.ExitCode -ne 0) {
        $detail = $result.Output.Trim()
        throw "PostgreSQL command failed: $detail"
    }
    $lines = @($result.Output -split "`r?`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    if ($lines.Count -eq 0) {
        throw "PostgreSQL command returned no value"
    }
    return $lines[0]
}

function Assert-Inventory {
    param(
        [long]$RoomTypeId,
        [datetime]$CheckIn,
        [datetime]$CheckOut,
        [int]$ExpectedRows,
        [int]$ExpectedBookedSum,
        [string]$Description
    )

    $value = Invoke-PsqlScalar @"
SELECT count(*)::text || '|' || coalesce(sum(rooms_booked), 0)::text
FROM room_type_availability
WHERE room_type_id = $RoomTypeId
  AND date >= '$($CheckIn.ToString("yyyy-MM-dd"))'
  AND date < '$($CheckOut.ToString("yyyy-MM-dd"))';
"@
    $parts = $value -split "\|"
    Assert-Equal ([int]$parts[0]) $ExpectedRows "$Description inventory row count is incorrect"
    Assert-Equal ([int]$parts[1]) $ExpectedBookedSum "$Description booked-room total is incorrect"
}

function Wait-ForTransactionalEmails {
    param(
        [long]$SuccessBookingId,
        [string]$SuccessEmail,
        [long]$RetryBookingId,
        [string]$RetryEmail,
        [int]$TimeoutSeconds
    )

    $expected = @(
        [PSCustomObject]@{ Email = $SuccessEmail; Subject = "Your Ryoko booking is confirmed" },
        [PSCustomObject]@{ Email = $SuccessEmail; Subject = "Your Ryoko booking was cancelled" },
        [PSCustomObject]@{ Email = $SuccessEmail; Subject = "Your Ryoko refund is complete" },
        [PSCustomObject]@{ Email = $RetryEmail; Subject = "Your Ryoko booking is confirmed" }
    )

    $timer = [Diagnostics.Stopwatch]::StartNew()
    $lastOutboxState = "0|0"
    $lastDeliveredCount = 0
    do {
        $lastOutboxState = Invoke-PsqlScalar @"
SELECT count(*) FILTER (WHERE status = 'sent')::text
    || '|'
    || count(*)::text
FROM email_outbox
WHERE booking_id IN ($SuccessBookingId, $RetryBookingId);
"@
        $outboxParts = $lastOutboxState -split "\|"
        $allOutboxRowsSent =
            [int]$outboxParts[0] -eq $expected.Count -and
            [int]$outboxParts[1] -eq $expected.Count

        $inbox = Invoke-RestMethod -Method Get -Uri "$script:MailpitBase/api/v1/messages"
        $messages = @($inbox.messages)
        $lastDeliveredCount = 0
        foreach ($wanted in $expected) {
            $matched = @($messages | Where-Object {
                $_.Subject -eq $wanted.Subject -and
                @($_.To | ForEach-Object { $_.Address }) -contains $wanted.Email
            }).Count -gt 0
            if ($matched) {
                $lastDeliveredCount++
            }
        }

        if ($allOutboxRowsSent -and $lastDeliveredCount -eq $expected.Count) {
            return
        }
        Start-Sleep -Seconds 1
    } while ($timer.Elapsed.TotalSeconds -lt $TimeoutSeconds)

    throw "Timed out waiting for transactional emails; outbox sent/total was '$lastOutboxState' and Mailpit contained $lastDeliveredCount of $($expected.Count) expected messages."
}

function Resolve-PostgresContainer {
    if ($PostgresContainer) {
        return $PostgresContainer
    }
    $compose = Invoke-NativeCapture -FilePath "docker" -Arguments @(
        "compose", "-f", $ComposeFile, "ps", "-q", $PostgresService
    )
    if ($compose.ExitCode -ne 0) {
        throw "Could not inspect the Docker test database"
    }
    $container = @($compose.Output -split "`r?`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ }) | Select-Object -First 1
    if (-not $container) {
        throw "Docker service '$PostgresService' is not running"
    }
    return $container
}

$apiUri = [Uri]$BaseUrl
$loopbackHosts = @("127.0.0.1", "localhost", "::1")
if (-not $AllowRemoteApi -and $apiUri.Host -notin $loopbackHosts) {
    throw "Refusing to run a destructive smoke test against non-local API '$BaseUrl'. Use -AllowRemoteApi only for an isolated test environment."
}
if ($WebhookTimeoutSeconds -lt 1 -or $RefundTimeoutSeconds -lt 1) {
    throw "Timeouts must be positive"
}
$script:ApiBase = $BaseUrl.TrimEnd("/")
$mailpitUri = [Uri]$MailpitBaseUrl
if ($mailpitUri.Host -notin $loopbackHosts) {
    throw "Refusing to inspect non-local Mailpit URL '$MailpitBaseUrl'."
}
$script:MailpitBase = $MailpitBaseUrl.TrimEnd("/")
if ($EmailTimeoutSeconds -lt 1) {
    throw "Email timeout must be positive"
}

Write-Step "Checking prerequisites"
foreach ($command in @("stripe", "docker")) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "Required command '$command' was not found"
    }
}
$sandbox = Invoke-NativeCapture -FilePath "stripe" -Arguments @("whoami")
if ($sandbox.ExitCode -ne 0 -or $sandbox.Output -notmatch "(?i)sandbox") {
    throw "Stripe CLI is not authenticated to a sandbox. Run 'stripe login' and select a sandbox account."
}
$ready = Invoke-WebRequest -UseBasicParsing "$script:ApiBase/health/ready" -TimeoutSec 5
Assert-Equal $ready.StatusCode 200 "Ryoko is not ready"
$mailpitReady = Invoke-WebRequest -UseBasicParsing "$script:MailpitBase/api/v1/messages" -TimeoutSec 5
Assert-Equal $mailpitReady.StatusCode 200 "Mailpit is not ready"
$script:PostgresContainer = Resolve-PostgresContainer

Write-Step "Creating an isolated hotel and room type in the Docker test database"
$fixtureStamp = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$roomTypeIdText = Invoke-PsqlScalar @"
WITH hotel AS (
    INSERT INTO hotels (name, address, city)
    VALUES ('Stripe Smoke Hotel $fixtureStamp', '1 Sandbox Street', 'Stripe Test City')
    RETURNING id
)
INSERT INTO room_types (hotel_id, name, price_per_night, capacity, total_rooms)
SELECT id, 'Stripe Smoke Room', 1000000.00, 2, 1
FROM hotel
RETURNING id;
"@
$roomTypeId = [long]$roomTypeIdText

Write-Step "Testing successful payment and refund"
$successUser = New-SmokeUserSession -Label "success"
$successToken = $successUser.AccessToken
$successCheckIn = [DateTime]::UtcNow.Date.AddDays(30)
$successCheckOut = $successCheckIn.AddDays(3)
$successBooking = New-SmokeBooking -RoomTypeId $roomTypeId -AccessToken $successToken `
    -CheckIn $successCheckIn -CheckOut $successCheckOut -IdempotencyKey "stripe-smoke-success-$fixtureStamp"
$successSession = New-PaymentSession -BookingId $successBooking.id -AccessToken $successToken
Assert-Equal ([long]$successSession.amount_minor) 3000000 "Payment amount is incorrect"
Assert-Inventory -RoomTypeId $roomTypeId -CheckIn $successCheckIn -CheckOut $successCheckOut `
    -ExpectedRows 3 -ExpectedBookedSum 3 -Description "Pending booking"

$confirmation = Confirm-StripePaymentIntent -PaymentIntentId $successSession.provider_payment_id -PaymentMethod "pm_card_visa"
if ($confirmation.ExitCode -ne 0) {
    throw "Stripe valid-card confirmation failed"
}
$paid = Wait-ForPayment -BookingId $successBooking.id -AccessToken $successToken `
    -TimeoutSeconds $WebhookTimeoutSeconds -Description "successful payment webhook" `
    -Condition { param($payment) $payment.status -eq "succeeded" }
Assert-True ([bool]$paid.paid_at) "Successful payment has no paid_at timestamp"
$confirmedBooking = Get-Booking -BookingId $successBooking.id -AccessToken $successToken
Assert-Equal $confirmedBooking.status "confirmed" "Paid booking was not confirmed"

$cancelled = Invoke-RyokoJson -Method Post -Path "/me/bookings/$($successBooking.id)/cancel" `
    -Headers (Get-AuthHeaders $successToken)
Assert-Equal $cancelled.status "cancelled" "Booking was not cancelled"
$queuedRefund = Get-Payment -BookingId $successBooking.id -AccessToken $successToken
Assert-True ($queuedRefund.status -in @("refund_pending", "refunded")) "Payment was not queued for refund"
Assert-Inventory -RoomTypeId $roomTypeId -CheckIn $successCheckIn -CheckOut $successCheckOut `
    -ExpectedRows 3 -ExpectedBookedSum 0 -Description "Cancelled booking"

$refunded = Wait-ForPayment -BookingId $successBooking.id -AccessToken $successToken `
    -TimeoutSeconds $RefundTimeoutSeconds -Description "Stripe refund worker" `
    -Condition { param($payment) $payment.status -eq "refunded" }
Assert-True ([bool]$refunded.refunded_at) "Refunded payment has no refunded_at timestamp"

$refunds = Invoke-NativeCapture -FilePath "stripe" -Arguments @(
    "refunds", "list", "--payment-intent", $successSession.provider_payment_id, "--limit", "1"
)
if ($refunds.ExitCode -ne 0) {
    throw "Could not verify the refund with Stripe"
}
$jsonStart = $refunds.Output.IndexOf("{")
if ($jsonStart -lt 0) {
    throw "Stripe refund lookup returned no JSON"
}
$stripeRefunds = $refunds.Output.Substring($jsonStart) | ConvertFrom-Json
Assert-True ($stripeRefunds.data.Count -gt 0) "Stripe has no refund for the successful PaymentIntent"
Assert-Equal $stripeRefunds.data[0].status "succeeded" "Stripe refund status is incorrect"
Assert-Equal ([long]$stripeRefunds.data[0].amount) 3000000 "Stripe refund amount is incorrect"

Write-Step "Testing declined payment and successful retry"
$retryUser = New-SmokeUserSession -Label "retry"
$retryToken = $retryUser.AccessToken
$retryCheckIn = [DateTime]::UtcNow.Date.AddDays(60)
$retryCheckOut = $retryCheckIn.AddDays(3)
$retryBooking = New-SmokeBooking -RoomTypeId $roomTypeId -AccessToken $retryToken `
    -CheckIn $retryCheckIn -CheckOut $retryCheckOut -IdempotencyKey "stripe-smoke-retry-$fixtureStamp"
$firstSession = New-PaymentSession -BookingId $retryBooking.id -AccessToken $retryToken
Confirm-StripePaymentIntent -PaymentIntentId $firstSession.provider_payment_id `
    -PaymentMethod "pm_card_visa_chargeDeclined" | Out-Null

$failed = Wait-ForPayment -BookingId $retryBooking.id -AccessToken $retryToken `
    -TimeoutSeconds $WebhookTimeoutSeconds -Description "failed payment webhook" `
    -Condition { param($payment) $payment.status -eq "pending" -and $payment.retryable }
Assert-Equal $failed.failure_code "payment_failed" "Customer-facing failure code is incorrect"
Assert-Inventory -RoomTypeId $roomTypeId -CheckIn $retryCheckIn -CheckOut $retryCheckOut `
    -ExpectedRows 3 -ExpectedBookedSum 3 -Description "Declined booking"

$secondSession = New-PaymentSession -BookingId $retryBooking.id -AccessToken $retryToken
Assert-Equal $secondSession.payment_id $firstSession.payment_id "Retry created a different payment row"
Assert-Equal $secondSession.provider_payment_id $firstSession.provider_payment_id "Retry created a different PaymentIntent"
$retryConfirmation = Confirm-StripePaymentIntent -PaymentIntentId $secondSession.provider_payment_id -PaymentMethod "pm_card_visa"
if ($retryConfirmation.ExitCode -ne 0) {
    throw "Stripe valid-card retry failed"
}

$retried = Wait-ForPayment -BookingId $retryBooking.id -AccessToken $retryToken `
    -TimeoutSeconds $WebhookTimeoutSeconds -Description "successful retry webhook" `
    -Condition { param($payment) $payment.status -eq "succeeded" }
Assert-True ([bool]$retried.paid_at) "Retried payment has no paid_at timestamp"
Assert-True (-not $retried.retryable) "Successful retry is still marked retryable"
Assert-True ($null -eq $retried.failure_code) "Successful retry retained a failure code"
$retriedBooking = Get-Booking -BookingId $retryBooking.id -AccessToken $retryToken
Assert-Equal $retriedBooking.status "confirmed" "Retried booking was not confirmed"
Assert-Inventory -RoomTypeId $roomTypeId -CheckIn $retryCheckIn -CheckOut $retryCheckOut `
    -ExpectedRows 3 -ExpectedBookedSum 3 -Description "Successfully retried booking"

Write-Step "Verifying transactional email delivery"
Wait-ForTransactionalEmails `
    -SuccessBookingId $successBooking.id -SuccessEmail $successUser.Email `
    -RetryBookingId $retryBooking.id -RetryEmail $retryUser.Email `
    -TimeoutSeconds $EmailTimeoutSeconds

Write-Host ""
Write-Host "Stripe sandbox smoke test passed" -ForegroundColor Green
[PSCustomObject]@{
    room_type_id              = $roomTypeId
    success_booking_id        = $successBooking.id
    refunded_payment_id       = $successSession.payment_id
    retry_booking_id          = $retryBooking.id
    retried_payment_id        = $firstSession.payment_id
    payment_amount_minor      = 3000000
    currency                  = "VND"
    successful_refund         = $true
    successful_decline_retry  = $true
    transactional_emails_sent = 4
} | Format-List
