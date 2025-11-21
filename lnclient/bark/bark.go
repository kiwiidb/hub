package bark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/getAlby/hub/lnclient"
)

var ErrNotImplemented = errors.New("not implemented")

const MSAT_PER_SAT = 1000

type BarkService struct {
	address    string
	httpClient *http.Client
}

func NewBarkService(ctx context.Context, address string) (*BarkService, error) {
	return &BarkService{
		address:    address,
		httpClient: &http.Client{},
	}, nil
}

// Lightning Pay types
type lightningPayRequest struct {
	Destination string  `json:"destination"`
	AmountSat   *int64  `json:"amount_sat,omitempty"`
	Comment     *string `json:"comment,omitempty"`
}

type lightningPayResponse struct {
	Message  string `json:"message"`
	Preimage string `json:"preimage"`
}

// Lightning Invoice types
type lightningInvoiceRequest struct {
	AmountSat int64 `json:"amount_sat"`
}

type invoiceInfo struct {
	Invoice string `json:"invoice"`
}

// Balance types
type walletBalance struct {
	SpendableSat               int64  `json:"spendable_sat"`
	PendingLightningSendSat    int64  `json:"pending_lightning_send_sat"`
	PendingLightningReceiveSat int64  `json:"pending_lightning_receive_sat"`
	PendingInRoundSat          int64  `json:"pending_in_round_sat"`
	PendingBoardSat            int64  `json:"pending_board_sat"`
	PendingExitSat             *int64 `json:"pending_exit_sat"`
}

type onchainBalance struct {
	TotalSat            int64 `json:"total_sat"`
	TrustedSpendableSat int64 `json:"trusted_spendable_sat"`
	ImmatureSat         int64 `json:"immature_sat"`
	TrustedPendingSat   int64 `json:"trusted_pending_sat"`
	UntrustedPendingSat int64 `json:"untrusted_pending_sat"`
	ConfirmedSat        int64 `json:"confirmed_sat"`
}

// Movement types
type movementSubsystem struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type movementDestination struct {
	Destination string `json:"destination"`
	AmountSat   int64  `json:"amount_sat"`
}

type movementTime struct {
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	CompletedAt *string `json:"completed_at"`
}

type movement struct {
	ID                  int                   `json:"id"`
	Status              string                `json:"status"`
	Subsystem           movementSubsystem     `json:"subsystem"`
	Metadata            string                `json:"metadata"`
	IntendedBalanceSat  int64                 `json:"intended_balance_sat"`
	EffectiveBalanceSat int64                 `json:"effective_balance_sat"`
	OffchainFeeSat      int64                 `json:"offchain_fee_sat"`
	SentTo              []movementDestination `json:"sent_to"`
	ReceivedOn          []movementDestination `json:"received_on"`
	InputVtxos          []string              `json:"input_vtxos"`
	OutputVtxos         []string              `json:"output_vtxos"`
	ExitedVtxos         []string              `json:"exited_vtxos"`
	Time                movementTime          `json:"time"`
}

// LNClient interface implementations

func (b *BarkService) SendPaymentSync(payReq string, amount *uint64) (*lnclient.PayInvoiceResponse, error) {
	var amountSat *int64
	if amount != nil {
		amt := int64(*amount)
		amountSat = &amt
	}

	req := lightningPayRequest{
		Destination: payReq,
		AmountSat:   amountSat,
	}

	var resp lightningPayResponse
	err := b.doRequest("POST", "/api/v1/lightning/pay", req, &resp)
	if err != nil {
		return nil, err
	}

	return &lnclient.PayInvoiceResponse{
		Preimage: resp.Preimage,
		Fee:      0, // Fee not provided in Bark response
	}, nil
}

func (b *BarkService) MakeInvoice(ctx context.Context, amount int64, description string, descriptionHash string, expiry int64, throughNodePubkey *string) (*lnclient.Transaction, error) {
	req := lightningInvoiceRequest{
		AmountSat: amount / MSAT_PER_SAT,
	}

	var resp invoiceInfo
	err := b.doRequest("POST", "/api/v1/lightning/receive/invoice", req, &resp)
	if err != nil {
		return nil, err
	}

	return &lnclient.Transaction{
		Type:        "incoming",
		Invoice:     resp.Invoice,
		Description: description,
		Amount:      amount,
	}, nil
}

func (b *BarkService) SendKeysend(amount uint64, destination string, customRecords []lnclient.TLVRecord, preimage string) (*lnclient.PayKeysendResponse, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) GetPubkey() string {
	return "0326e692c455dd554c709bbb470b0ca7e0bb04152f777d1445fd0bf3709a2833a3"
}

func (b *BarkService) GetInfo(ctx context.Context) (*lnclient.NodeInfo, error) {
	return &lnclient.NodeInfo{
		Alias:       "allNice | torq.co | second.tech",
		Color:       "",
		Pubkey:      "0326e692c455dd554c709bbb470b0ca7e0bb04152f777d1445fd0bf3709a2833a3",
		Network:     "mainnet",
		BlockHeight: 0,
		BlockHash:   "",
	}, nil
}

func (b *BarkService) MakeHoldInvoice(ctx context.Context, amount int64, description string, descriptionHash string, expiry int64, paymentHash string) (*lnclient.Transaction, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) SettleHoldInvoice(ctx context.Context, preimage string) error {
	return ErrNotImplemented
}

func (b *BarkService) CancelHoldInvoice(ctx context.Context, paymentHash string) error {
	return ErrNotImplemented
}

func (b *BarkService) LookupInvoice(ctx context.Context, paymentHash string) (*lnclient.Transaction, error) {
	type lightningStatusResponse struct {
		PaymentHash        string  `json:"payment_hash"`
		PaymentPreimage    string  `json:"payment_preimage"`
		Invoice            string  `json:"invoice"`
		PreimageRevealedAt *string `json:"preimage_revealed_at"`
	}

	var resp lightningStatusResponse
	endpoint := fmt.Sprintf("/api/v1/lightning/receive/status?filter=%s", paymentHash)
	if err := b.doRequest("GET", endpoint, nil, &resp); err != nil {
		return nil, fmt.Errorf("failed to lookup invoice: %w", err)
	}

	// Parse the invoice to get the amount (simplified - you may want to use a proper bolt11 parser)
	var settledAt *int64
	if resp.PreimageRevealedAt != nil {
		revealedTime, err := time.Parse(time.RFC3339, *resp.PreimageRevealedAt)
		if err == nil {
			settledAtUnix := revealedTime.Unix()
			settledAt = &settledAtUnix
		}
	}

	return &lnclient.Transaction{
		Type:        "incoming",
		Invoice:     resp.Invoice,
		Preimage:    resp.PaymentPreimage,
		PaymentHash: resp.PaymentHash,
		SettledAt:   settledAt,
	}, nil
}

func (b *BarkService) ListTransactions(ctx context.Context, from, until, limit, offset uint64, unpaid bool, invoiceType string) ([]lnclient.Transaction, error) {
	var movements []movement
	if err := b.doRequest("GET", "/api/v1/movements", nil, &movements); err != nil {
		return nil, fmt.Errorf("failed to get movements: %w", err)
	}

	transactions := make([]lnclient.Transaction, 0)
	for _, m := range movements {
		// Parse timestamps
		createdAt, err := time.Parse(time.RFC3339, m.Time.CreatedAt)
		if err != nil {
			continue
		}
		createdAtUnix := createdAt.Unix()

		var settledAt *int64
		if m.Time.CompletedAt != nil && m.Status == "finished" {
			completedTime, err := time.Parse(time.RFC3339, *m.Time.CompletedAt)
			if err == nil {
				settledAtUnix := completedTime.Unix()
				settledAt = &settledAtUnix
			}
		}

		// Determine transaction type and extract invoice/amount
		var txType string
		var invoice string
		var amount int64

		switch m.Subsystem.Kind {
		case "receive":
			txType = "incoming"
			if len(m.ReceivedOn) > 0 {
				invoice = m.ReceivedOn[0].Destination
				amount = m.ReceivedOn[0].AmountSat * MSAT_PER_SAT
			}
		case "send":
			txType = "outgoing"
			if len(m.SentTo) > 0 {
				invoice = m.SentTo[0].Destination
				amount = m.SentTo[0].AmountSat * MSAT_PER_SAT
			}
		default:
			continue // Skip non-lightning transactions
		}

		transactions = append(transactions, lnclient.Transaction{
			Type:      txType,
			Invoice:   invoice,
			Amount:    amount,
			FeesPaid:  m.OffchainFeeSat * MSAT_PER_SAT,
			CreatedAt: createdAtUnix,
			SettledAt: settledAt,
		})
	}

	return transactions, nil
}

func (b *BarkService) ListOnchainTransactions(ctx context.Context) ([]lnclient.OnchainTransaction, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) Shutdown() error {
	return ErrNotImplemented
}

func (b *BarkService) ListChannels(ctx context.Context) ([]lnclient.Channel, error) {
	return []lnclient.Channel{}, nil
}

func (b *BarkService) GetNodeConnectionInfo(ctx context.Context) (*lnclient.NodeConnectionInfo, error) {
	return &lnclient.NodeConnectionInfo{
		Pubkey:  "0326e692c455dd554c709bbb470b0ca7e0bb04152f777d1445fd0bf3709a2833a3",
		Address: "57.129.59.146",
		Port:    9735,
	}, nil
}

func (b *BarkService) GetNodeStatus(ctx context.Context) (*lnclient.NodeStatus, error) {
	return &lnclient.NodeStatus{
		IsReady:            true,
		InternalNodeStatus: nil,
	}, nil
}

func (b *BarkService) ConnectPeer(ctx context.Context, connectPeerRequest *lnclient.ConnectPeerRequest) error {
	return ErrNotImplemented
}

func (b *BarkService) OpenChannel(ctx context.Context, openChannelRequest *lnclient.OpenChannelRequest) (*lnclient.OpenChannelResponse, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) CloseChannel(ctx context.Context, closeChannelRequest *lnclient.CloseChannelRequest) (*lnclient.CloseChannelResponse, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) UpdateChannel(ctx context.Context, updateChannelRequest *lnclient.UpdateChannelRequest) error {
	return ErrNotImplemented
}

func (b *BarkService) DisconnectPeer(ctx context.Context, peerId string) error {
	return ErrNotImplemented
}

func (b *BarkService) MakeOffer(ctx context.Context, description string) (string, error) {
	return "", ErrNotImplemented
}

func (b *BarkService) GetNewOnchainAddress(ctx context.Context) (string, error) {
	return "", ErrNotImplemented
}

func (b *BarkService) ResetRouter(key string) error {
	return ErrNotImplemented
}

func (b *BarkService) GetOnchainBalance(ctx context.Context) (*lnclient.OnchainBalanceResponse, error) {
	var onchainBal onchainBalance

	if err := b.doRequest("GET", "/api/v1/onchain/balance", nil, &onchainBal); err != nil {
		return nil, fmt.Errorf("failed to get onchain balance: %w", err)
	}

	return &lnclient.OnchainBalanceResponse{
		Spendable: onchainBal.TrustedSpendableSat * MSAT_PER_SAT,
		Total:     onchainBal.TotalSat * MSAT_PER_SAT,
		Reserved:  onchainBal.ImmatureSat * MSAT_PER_SAT,
	}, nil
}

func (b *BarkService) GetBalances(ctx context.Context, includeInactiveChannels bool) (*lnclient.BalancesResponse, error) {
	var walletBal walletBalance
	var onchainBal onchainBalance

	// Fetch wallet balance
	if err := b.doRequest("GET", "/api/v1/wallet/balance", nil, &walletBal); err != nil {
		return nil, fmt.Errorf("failed to get wallet balance: %w", err)
	}

	// Fetch onchain balance
	if err := b.doRequest("GET", "/api/v1/onchain/balance", nil, &onchainBal); err != nil {
		return nil, fmt.Errorf("failed to get onchain balance: %w", err)
	}

	return &lnclient.BalancesResponse{
		Onchain: lnclient.OnchainBalanceResponse{
			Spendable: onchainBal.TrustedSpendableSat * MSAT_PER_SAT,
			Total:     onchainBal.TotalSat * MSAT_PER_SAT,
			Reserved:  onchainBal.ImmatureSat * MSAT_PER_SAT,
		},
		Lightning: lnclient.LightningBalanceResponse{
			TotalSpendable:       walletBal.SpendableSat * MSAT_PER_SAT,
			TotalReceivable:      0, // Not provided by Bark API
			NextMaxSpendable:     walletBal.SpendableSat * MSAT_PER_SAT,
			NextMaxReceivable:    0,
			NextMaxSpendableMPP:  walletBal.SpendableSat * MSAT_PER_SAT,
			NextMaxReceivableMPP: 0,
		},
	}, nil
}

func (b *BarkService) RedeemOnchainFunds(ctx context.Context, toAddress string, amount uint64, feeRate *uint64, sendAll bool) (string, error) {
	return "", ErrNotImplemented
}

func (b *BarkService) SendPaymentProbes(ctx context.Context, invoice string) error {
	return ErrNotImplemented
}

func (b *BarkService) SendSpontaneousPaymentProbes(ctx context.Context, amountMsat uint64, nodeId string) error {
	return ErrNotImplemented
}

func (b *BarkService) ListPeers(ctx context.Context) ([]lnclient.PeerDetails, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) GetLogOutput(ctx context.Context, maxLen int) ([]byte, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) SignMessage(ctx context.Context, message string) (string, error) {
	return "", ErrNotImplemented
}

func (b *BarkService) GetStorageDir() (string, error) {
	return "", ErrNotImplemented
}

func (b *BarkService) GetNetworkGraph(ctx context.Context, nodeIds []string) (lnclient.NetworkGraphResponse, error) {
	return nil, ErrNotImplemented
}

func (b *BarkService) UpdateLastWalletSyncRequest() {
	// No-op
}

func (b *BarkService) GetSupportedNIP47Methods() []string {
	return []string{"pay_invoice", "make_invoice", "get_balance", "list_transactions", "lookup_invoice"}
}

func (b *BarkService) GetSupportedNIP47NotificationTypes() []string {
	return []string{}
}

func (b *BarkService) GetCustomNodeCommandDefinitions() []lnclient.CustomNodeCommandDef {
	return []lnclient.CustomNodeCommandDef{}
}

func (b *BarkService) ExecuteCustomNodeCommand(ctx context.Context, command *lnclient.CustomNodeCommandRequest) (*lnclient.CustomNodeCommandResponse, error) {
	return nil, lnclient.ErrUnknownCustomNodeCommand
}

// doRequest performs an HTTP request to the Bark API
func (b *BarkService) doRequest(method, path string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, b.address+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}
