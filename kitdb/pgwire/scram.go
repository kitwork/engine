package pgwire

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// SCRAMVerifier is what the server keeps of a password for SCRAM-SHA-256 (RFC 5802, RFC 7677): never the password,
// only its salt, the iteration count of PBKDF2, and the two keys derived from the salted password.
type SCRAMVerifier struct {
	Salt       []byte
	Iterations int
	StoredKey  [32]byte
	ServerKey  [32]byte
}

// SCRAMVerifierFromSaltedPassword derives the verifier from SCRAM's SaltedPassword, PBKDF2-HMAC-SHA256 of the
// password with salt and iterations (what kitdb/managed keeps as a user's verifier).
func SCRAMVerifierFromSaltedPassword(salt []byte, iterations int, saltedPassword []byte) SCRAMVerifier {
	clientKey := scramHMAC(saltedPassword, "Client Key")
	verifier := SCRAMVerifier{Salt: append([]byte(nil), salt...), Iterations: iterations}
	verifier.StoredKey = sha256.Sum256(clientKey)
	copy(verifier.ServerKey[:], scramHMAC(saltedPassword, "Server Key"))
	return verifier
}

func scramHMAC(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

// SCRAMAuthenticator authenticates with SCRAM-SHA-256 instead of a cleartext password. The server asks it for the
// verifier of the startup's user (found false for an unknown or disabled user: the exchange still runs on a mock
// verifier and fails at the proof, so a client cannot tell an unknown user from a wrong password), checks the
// client's proof, and only then opens the session. An Authenticator that is also a SCRAMAuthenticator is asked with
// SCRAM; any other keeps the cleartext exchange.
type SCRAMAuthenticator interface {
	SCRAMVerifier(ctx context.Context, startup Startup) (verifier SCRAMVerifier, found bool, err error)
	OpenSession(ctx context.Context, startup Startup) (Session, error)
}

const (
	authenticationSASL         = 10
	authenticationSASLContinue = 11
	authenticationSASLFinal    = 12
	scramMechanism             = "SCRAM-SHA-256"
	scramPlusMechanism         = "SCRAM-SHA-256-PLUS"
	scramChannelBindingType    = "tls-server-end-point"
	scramServerNonceBytes      = 18
	scramMockSaltBytes         = 16
)

// usable reports whether a verifier can be offered to a client at all; one that cannot is answered like an
// unknown user's, so a broken account fails closed and looks like any other failure.
func (verifier SCRAMVerifier) usable() bool {
	return verifier.Iterations > 0 && len(verifier.Salt) > 0
}

// authenticateSCRAM runs SCRAM-SHA-256 as PostgreSQL does, and SCRAM-SHA-256-PLUS over TLS:
//
//	server: AuthenticationSASL (10), the mechanisms: SCRAM-SHA-256-PLUS first over TLS, then SCRAM-SHA-256
//	client: SASLInitialResponse, the mechanism and client-first-message "<gs2 header>n=<ignored>,r=<client nonce>"
//	server: AuthenticationSASLContinue (11), server-first-message "r=<client nonce><server nonce>,s=<salt>,i=<n>"
//	client: SASLResponse, client-final-message "c=<channel binding>,r=<both nonces>,p=<proof>"
//	server: AuthenticationSASLFinal (12), server-final-message "v=<server signature>"
//
// The gs2 header is "n,," from a client without channel binding, "y,," from one that has it but was not offered it,
// and "p=tls-server-end-point,," with SCRAM-SHA-256-PLUS; c= is base64 of the header, followed for PLUS by the hash of
// the server's certificate as the client saw it (serverEndPoint). The proof covers c=, so a relay that ends the
// client's TLS with its own certificate and opens another TLS to this server cannot carry a PLUS exchange through:
// the hash the client sends is of the relay's certificate, not of this server's. A client that has channel binding
// and says "y" while this server offered PLUS was told otherwise by someone between them, and is refused.
//
// The user is the startup's; the name inside the client's first message is ignored. An unknown user runs the same
// exchange on a mock verifier and fails at the proof with a wrong password's error. The session is opened only once
// the proof verifies. A failure has been sent to the client when it returns an error.
func (wire *wireConnection) authenticateSCRAM(ctx context.Context, authenticator SCRAMAuthenticator) (Session, error) {
	user := wire.startup.User()
	verifier, found, err := authenticator.SCRAMVerifier(ctx, wire.startup)
	if err != nil {
		return nil, wire.refuseAuthentication(err)
	}
	if found && !verifier.usable() {
		found = false
	}
	if !found {
		verifier = wire.runtime.mockSCRAMVerifier(user)
	}
	serverNonce, err := newSCRAMServerNonce()
	if err != nil {
		_ = wire.sendError(NewError("XX000", "could not generate a SCRAM nonce"))
		return nil, err
	}
	exchange := scramExchange{verifier: verifier, found: found, serverNonce: serverNonce, offered: wire.channelBinding != nil}

	var request encoder
	request.int32(authenticationSASL)
	if exchange.offered {
		request.cstring(scramPlusMechanism)
	}
	request.cstring(scramMechanism)
	request.byte(0)
	if err := wire.send('R', request); err != nil {
		return nil, err
	}
	if err := wire.flush(); err != nil {
		return nil, err
	}

	body, err := wire.readSASLResponse()
	if err != nil {
		return nil, err
	}
	initial := decoder{data: body}
	mechanism, err := initial.cstring()
	if err != nil {
		return nil, wire.refuseAuthentication(NewError("08P01", "invalid SASLInitialResponse message"))
	}
	length, err := initial.int32()
	if err != nil {
		return nil, wire.refuseAuthentication(NewError("08P01", "invalid SASLInitialResponse message"))
	}
	switch {
	case mechanism == scramPlusMechanism && exchange.offered:
		exchange.binding = wire.channelBinding
	case mechanism == scramMechanism:
	default:
		return nil, wire.refuseAuthentication(NewError("08P01", "client selected an invalid SASL authentication mechanism"))
	}
	if length < 0 {
		return nil, wire.refuseAuthentication(NewError("08P01", "malformed SCRAM message: the client-first-message is missing"))
	}
	clientFirst, err := initial.bytes(int(length))
	if err != nil || initial.done() != nil {
		return nil, wire.refuseAuthentication(NewError("08P01", "invalid SASLInitialResponse message"))
	}
	serverFirst, err := exchange.receiveClientFirst(clientFirst)
	if err != nil {
		return nil, wire.refuseAuthentication(err)
	}
	challenge := encoder{}
	challenge.int32(authenticationSASLContinue)
	challenge = append(challenge, serverFirst...)
	if err := wire.send('R', challenge); err != nil {
		return nil, err
	}
	if err := wire.flush(); err != nil {
		return nil, err
	}

	clientFinal, err := wire.readSASLResponse()
	if err != nil {
		return nil, err
	}
	serverFinal, authenticated, err := exchange.receiveClientFinal(clientFinal)
	if err != nil {
		return nil, wire.refuseAuthentication(err)
	}
	if !authenticated {
		return nil, wire.refuseAuthentication(NewError("28P01", "password authentication failed for user \""+user+"\""))
	}
	final := encoder{}
	final.int32(authenticationSASLFinal)
	final = append(final, serverFinal...)
	if err := wire.send('R', final); err != nil {
		return nil, err
	}

	session, err := authenticator.OpenSession(ctx, wire.startup)
	if err != nil {
		return nil, wire.refuseAuthentication(err)
	}
	return session, nil
}

// readSASLResponse reads one SASLInitialResponse or SASLResponse, bounded tighter than an ordinary message.
func (wire *wireConnection) readSASLResponse() ([]byte, error) {
	maxBytes := min(wire.runtime.server.MaxMessageBytes, maxSASLMessageBytes)
	kind, body, err := wire.readBoundedMessage(wire.runtime.server.IdleTimeout, maxBytes)
	if err != nil {
		return nil, err
	}
	if kind != 'p' {
		return nil, wire.refuseAuthentication(NewError("08P01", fmt.Sprintf("expected SASL response, got message type %q", kind)))
	}
	return body, nil
}

func (wire *wireConnection) refuseAuthentication(err error) error {
	_ = wire.sendError(err)
	return err
}

func newSCRAMServerNonce() (string, error) {
	var random [scramServerNonceBytes]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(random[:]), nil
}

// mockSCRAMVerifier answers an unknown or disabled user. Its salt and keys come from the user name and the server's
// secret, so the same name gets the same salt each time while the server runs, and its iteration count is a real
// account's. No password is known to match it, and the exchange fails at the proof whatever the proof is.
func (runtime *serverRuntime) mockSCRAMVerifier(user string) SCRAMVerifier {
	derive := func(label string) []byte {
		mac := hmac.New(sha256.New, runtime.scramMockSecret[:])
		mac.Write([]byte(label))
		mac.Write([]byte{0})
		mac.Write([]byte(user))
		return mac.Sum(nil)
	}
	verifier := SCRAMVerifier{
		Salt:       derive("salt")[:scramMockSaltBytes],
		Iterations: runtime.server.SCRAMMockIterations,
	}
	copy(verifier.StoredKey[:], derive("stored key"))
	copy(verifier.ServerKey[:], derive("server key"))
	return verifier
}

// scramExchange is the server's side of one SCRAM-SHA-256 exchange (RFC 5802 section 5, RFC 7677).
type scramExchange struct {
	verifier SCRAMVerifier
	// found is false when verifier is a mock one: the exchange then fails at the proof.
	found       bool
	serverNonce string
	// offered is true when the server offered SCRAM-SHA-256-PLUS (over TLS); binding is the certificate's hash the
	// client must send back when it chose PLUS, nil for SCRAM-SHA-256
	offered bool
	binding []byte

	gs2Header       string
	nonce           string // the client's nonce followed by the server's
	clientFirstBare string
	serverFirst     string
}

func malformedSCRAM(reason string) error {
	return NewError("08P01", "malformed SCRAM message: "+reason)
}

// receiveClientFirst reads the client-first-message and returns the server-first-message.
func (exchange *scramExchange) receiveClientFirst(message []byte) ([]byte, error) {
	text := string(message)
	// The gs2 header: a channel-binding flag and an optional authorization identity, each followed by a comma.
	flag, rest, ok := strings.Cut(text, ",")
	if !ok {
		return nil, malformedSCRAM("the gs2 header is incomplete")
	}
	switch {
	case (flag == "n" || flag == "y") && exchange.binding != nil:
		return nil, malformedSCRAM("the client selected SCRAM-SHA-256-PLUS, but the SCRAM message does not include channel binding data")
	case flag == "n":
	case flag == "y":
		if exchange.offered {
			return nil, NewError("08P01", "SCRAM channel binding negotiation error: the client supports SCRAM channel binding "+
				"but thinks the server does not; this server does support channel binding")
		}
	case strings.HasPrefix(flag, "p="):
		if exchange.binding == nil {
			return nil, malformedSCRAM("the client selected SCRAM-SHA-256 without channel binding, but the SCRAM message includes channel binding data")
		}
		if kind := flag[len("p="):]; kind != scramChannelBindingType {
			if !scramPrintable(kind) {
				kind = "?"
			}
			return nil, NewError("08P01", "unsupported SCRAM channel-binding type \""+kind+"\"")
		}
	default:
		return nil, malformedSCRAM("unexpected channel-binding flag")
	}
	authorization, bare, ok := strings.Cut(rest, ",")
	if !ok {
		return nil, malformedSCRAM("the gs2 header is incomplete")
	}
	if strings.HasPrefix(authorization, "a=") {
		return nil, NewError("0A000", "client uses authorization identity, but it is not supported")
	}
	if authorization != "" {
		return nil, malformedSCRAM("unexpected attribute in the gs2 header")
	}
	attributes := strings.Split(bare, ",")
	if strings.HasPrefix(attributes[0], "m=") {
		return nil, NewError("0A000", "client requires an unsupported SCRAM extension")
	}
	if len(attributes) < 2 || !strings.HasPrefix(attributes[0], "n=") || !strings.HasPrefix(attributes[1], "r=") {
		return nil, malformedSCRAM("the client-first-message needs a user name and a nonce")
	}
	clientNonce := attributes[1][len("r="):]
	if !scramPrintable(clientNonce) {
		return nil, malformedSCRAM("the client nonce is not printable")
	}
	for _, extension := range attributes[2:] {
		if !scramAttribute(extension) {
			return nil, malformedSCRAM("invalid attribute in the client-first-message")
		}
	}
	exchange.gs2Header = flag + ",,"
	exchange.clientFirstBare = bare
	exchange.nonce = clientNonce + exchange.serverNonce
	exchange.serverFirst = "r=" + exchange.nonce +
		",s=" + base64.StdEncoding.EncodeToString(exchange.verifier.Salt) +
		",i=" + strconv.Itoa(exchange.verifier.Iterations)
	return []byte(exchange.serverFirst), nil
}

// receiveClientFinal reads the client-final-message. A malformed or tampered message is an error; a proof that
// does not verify (or any proof against a mock verifier) is authenticated=false; otherwise it returns the
// server-final-message.
func (exchange *scramExchange) receiveClientFinal(message []byte) (serverFinal []byte, authenticated bool, err error) {
	text := string(message)
	attributes := strings.Split(text, ",")
	last := attributes[len(attributes)-1]
	if len(attributes) < 3 || !strings.HasPrefix(attributes[0], "c=") || !strings.HasPrefix(attributes[1], "r=") ||
		!strings.HasPrefix(last, "p=") {
		return nil, false, malformedSCRAM("the client-final-message needs channel binding, a nonce and a proof, the proof last")
	}
	for _, extension := range attributes[2 : len(attributes)-1] {
		if !scramAttribute(extension) {
			return nil, false, malformedSCRAM("invalid attribute in the client-final-message")
		}
	}
	if attributes[0][len("c="):] != base64.StdEncoding.EncodeToString(append([]byte(exchange.gs2Header), exchange.binding...)) {
		if exchange.binding != nil {
			return nil, false, NewError("28000", "SCRAM channel binding check failed")
		}
		return nil, false, NewError("08P01", "unexpected SCRAM channel-binding attribute in client-final-message")
	}
	if subtle.ConstantTimeCompare([]byte(attributes[1][len("r="):]), []byte(exchange.nonce)) != 1 {
		return nil, false, NewError("08P01", "invalid SCRAM response: the nonce does not match")
	}
	proof, decodeErr := base64.StdEncoding.DecodeString(last[len("p="):])
	if decodeErr != nil || len(proof) != sha256.Size {
		return nil, false, malformedSCRAM("malformed proof in client-final-message")
	}

	withoutProof := text[:len(text)-len(last)-1]
	authMessage := exchange.clientFirstBare + "," + exchange.serverFirst + "," + withoutProof
	clientSignature := scramHMAC(exchange.verifier.StoredKey[:], authMessage)
	clientKey := make([]byte, sha256.Size)
	for index := range clientKey {
		clientKey[index] = proof[index] ^ clientSignature[index]
	}
	storedKey := sha256.Sum256(clientKey)
	// The proof is checked even against a mock verifier, so an unknown user costs the same time as a wrong password.
	if !hmac.Equal(storedKey[:], exchange.verifier.StoredKey[:]) || !exchange.found {
		return nil, false, nil
	}
	serverSignature := scramHMAC(exchange.verifier.ServerKey[:], authMessage)
	return []byte("v=" + base64.StdEncoding.EncodeToString(serverSignature)), true, nil
}

// scramPrintable reports whether value is a non-empty run of printable ASCII without a comma (RFC 5802's nonce).
func scramPrintable(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character < 0x21 || character > 0x7e || character == ',' {
			return false
		}
	}
	return true
}

// scramAttribute reports whether value has an attribute's shape: one ASCII letter, '=', then the value.
func scramAttribute(value string) bool {
	if len(value) < 2 || value[1] != '=' {
		return false
	}
	letter := value[0]
	return (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
}
