package lnwire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/roasbeef/btcd/btcec"
	"github.com/roasbeef/btcd/txscript"
	"github.com/roasbeef/btcd/wire"
	"github.com/roasbeef/btcutil"
)

// MaxSliceLength is the maximum allowed lenth for any opaque byte slices in
// the wire protocol.
const MaxSliceLength = 65535

// PkScript is simple type definition which represents a raw serialized public
// key script.
type PkScript []byte

// HTLCKey is an identifier used to uniquely identify any HTLC's transmitted
// between Alice and Bob. In order to cancel, timeout, or settle HTLC's this
// identifier should be used to allow either side to easily locate and modify
// any staged or pending HTLCs.
// TODO(roasbeef): change to HTLCIdentifier?
type HTLCKey int64

// CommitHeight is an integer which represents the highest HTLCKey seen by
// either side within their commitment transaction. Any addition to the pending,
// HTLC lists on either side will increment this height. As a result this value
// should always be monotonically increasing. Any CommitSignature or
// CommitRevocation messages will reference a value for the commitment height
// up to which it covers. HTLC's are only explicltly excluded by sending
// HTLCReject messages referencing a particular HTLCKey.
type CommitHeight uint64

// CreditsAmount are the native currency unit used within the Lightning Network.
// Credits are denominated in sub-satoshi amounts, so micro-satoshis (1/1000).
// This value is purposefully signed in order to allow the expression of negative
// fees.
//
// "In any science-fiction movie, anywhere in the galaxy, currency is referred
// to as 'credits.'"
// 	--Sam Humphries. Ebert, Roger (1999). Ebert's bigger little movie
// 	glossary. Andrews McMeel. p. 172.
//
// https://en.wikipedia.org/wiki/List_of_fictional_currencies
// https://en.wikipedia.org/wiki/Fictional_currency#Trends_in_the_use_of_fictional_currencies
// http://tvtropes.org/pmwiki/pmwiki.php/Main/WeWillSpendCreditsInTheFuture
// US Display format: 1 BTC = 100,000,000'000 XCB
// Or in BTC = 1.00000000'000
// Credits (XCB, accountants should use XCB :^)
type CreditsAmount int64

// ToSatoshi converts an amount in Credits to the coresponding amount
// expressed in Satoshis.
//
// NOTE: This function rounds down by default (floor).
func (c CreditsAmount) ToSatoshi() int64 {
	return int64(c / 1000)
}

// Writes the big endian representation of element
// Unified function to call when writing different types
// Pre-allocate a byte-array of the correct size for cargo-cult security
// More copies but whatever...
func writeElement(w io.Writer, element interface{}) error {
	var err error
	switch e := element.(type) {
	case uint8:
		var b [1]byte
		b[0] = byte(e)
		_, err = w.Write(b[:])
		if err != nil {
			return err
		}
		return nil
	case uint16:
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(e))
		_, err = w.Write(b[:])
		if err != nil {
			return err
		}
		return nil
	case CreditsAmount:
		err = binary.Write(w, binary.BigEndian, int64(e))
		if err != nil {
			return err
		}
		return nil
	case uint32:
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(e))
		_, err = w.Write(b[:])
		if err != nil {
			return err
		}
		return nil
	case uint64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(e))
		_, err = w.Write(b[:])
		if err != nil {
			return err
		}
		return nil
	case HTLCKey:
		err = binary.Write(w, binary.BigEndian, int64(e))
		if err != nil {
			return err
		}
		return nil
	case btcutil.Amount:
		err = binary.Write(w, binary.BigEndian, int64(e))
		if err != nil {
			return err
		}
		return nil
	case *btcec.PublicKey:
		var b [33]byte
		serializedPubkey := e.SerializeCompressed()
		if len(serializedPubkey) != 33 {
			return fmt.Errorf("Wrong size pubkey")
		}
		copy(b[:], serializedPubkey)
		_, err = w.Write(b[:])
		if err != nil {
			return err
		}
		return nil
	case []uint64:
		numItems := len(e)
		if numItems > 65535 {
			return fmt.Errorf("Too many []uint64s")
		}
		// Write the size
		err = writeElement(w, uint16(numItems))
		if err != nil {
			return err
		}
		// Write the data
		for i := 0; i < numItems; i++ {
			err = writeElement(w, e[i])
			if err != nil {
				return err
			}
		}
		return nil
	case []*btcec.Signature:
		numSigs := len(e)
		if numSigs > 127 {
			return fmt.Errorf("Too many signatures!")
		}
		// Write the size
		err = writeElement(w, uint8(numSigs))
		if err != nil {
			return err
		}
		// Write the data
		for i := 0; i < numSigs; i++ {
			err = writeElement(w, e[i])
			if err != nil {
				return err
			}
		}
		return nil
	case *btcec.Signature:
		sig := e.Serialize()
		sigLength := len(sig)
		if sigLength > 73 {
			return fmt.Errorf("Signature too long!")
		}
		// Write the size
		err = writeElement(w, uint8(sigLength))
		if err != nil {
			return err
		}
		// Write the data
		_, err = w.Write(sig)
		if err != nil {
			return err
		}
		return nil
	case *wire.ShaHash:
		_, err = w.Write(e[:])
		if err != nil {
			return err
		}
		return nil
	case []*[20]byte:
		// Get size of slice and dump in slice
		sliceSize := len(e)
		err = writeElement(w, uint16(sliceSize))
		if err != nil {
			return err
		}
		// Write in each sequentially
		for _, element := range e {
			err = writeElement(w, &element)
			if err != nil {
				return err
			}
		}
		return nil
	case **[20]byte:
		_, err = w.Write((*e)[:])
		if err != nil {
			return err
		}
	case [20]byte:
		_, err = w.Write(e[:])
		if err != nil {
			return err
		}
		return nil
	case wire.BitcoinNet:
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(e))
		_, err := w.Write(b[:])
		if err != nil {
			return err
		}
		return nil
	case []byte:
		sliceLength := len(e)
		if sliceLength > MAX_SLICE_LENGTH {
			return fmt.Errorf("Slice length too long!")
		}
		// Write the size
		err = writeElement(w, uint16(sliceLength))
		if err != nil {
			return err
		}
		// Write the data
		_, err = w.Write(e)
		if err != nil {
			return err
		}
		return nil
	case PkScript:
		scriptLength := len(e)
		// Make sure it's P2PKH or P2SH size or less
		if scriptLength > 25 {
			return fmt.Errorf("PkScript too long!")
		}
		// Write the size (1-byte)
		err = writeElement(w, uint8(scriptLength))
		if err != nil {
			return err
		}
		// Write the data
		_, err = w.Write(e)
		if err != nil {
			return err
		}
		return nil
	case string:
		strlen := len(e)
		if strlen > 65535 {
			return fmt.Errorf("String too long!")
		}
		// Write the size (2-bytes)
		err = writeElement(w, uint16(strlen))
		if err != nil {
			return err
		}
		// Write the data
		_, err = w.Write([]byte(e))
		if err != nil {
			return err
		}
	case []*wire.TxIn:
		// Append the unsigned(!!!) txins
		// Write the size (1-byte)
		if len(e) > 127 {
			return fmt.Errorf("Too many txins")
		}
		err = writeElement(w, uint8(len(e)))
		if err != nil {
			return err
		}
		// Append the actual TxIns (Size: NumOfTxins * 36)
		// Do not include the sequence number to eliminate funny business
		for _, in := range e {
			err = writeElement(w, in)
			if err != nil {
				return err
			}
		}
		return nil
	case *wire.TxIn:
		// Hash
		var h [32]byte
		copy(h[:], e.PreviousOutPoint.Hash.Bytes())
		_, err = w.Write(h[:])
		if err != nil {
			return err
		}
		// Index
		var idx [4]byte
		binary.BigEndian.PutUint32(idx[:], e.PreviousOutPoint.Index)
		_, err = w.Write(idx[:])
		if err != nil {
			return err
		}
		return nil

	default:
		return fmt.Errorf("Unknown type in writeElement: %T", e)
	}

	return nil
}

func writeElements(w io.Writer, elements ...interface{}) error {
	for _, element := range elements {
		err := writeElement(w, element)
		if err != nil {
			return err
		}
	}
	return nil
}

func readElement(r io.Reader, element interface{}) error {
	var err error
	switch e := element.(type) {
	case *uint8:
		var b [1]uint8
		_, err = r.Read(b[:])
		if err != nil {
			return err
		}
		*e = b[0]
		return nil
	case *uint16:
		var b [2]byte
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = binary.BigEndian.Uint16(b[:])
		return nil
	case *CreditsAmount:
		var b [8]byte
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = CreditsAmount(int64(binary.BigEndian.Uint64(b[:])))
		return nil
	case *uint32:
		var b [4]byte
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = binary.BigEndian.Uint32(b[:])
		return nil
	case *uint64:
		var b [8]byte
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = binary.BigEndian.Uint64(b[:])
		return nil
	case *HTLCKey:
		var b [8]byte
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = HTLCKey(int64(binary.BigEndian.Uint64(b[:])))
		return nil
	case *btcutil.Amount:
		var b [8]byte
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = btcutil.Amount(int64(binary.BigEndian.Uint64(b[:])))
		return nil
	case **wire.ShaHash:
		var b wire.ShaHash
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = &b
		return nil
	case **btcec.PublicKey:
		var b [33]byte
		_, err = io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		x, err := btcec.ParsePubKey(b[:], btcec.S256())
		if err != nil {
			return err
		}
		*e = x
		return nil
	case *[]uint64:
		var numItems uint16
		err = readElement(r, &numItems)
		if err != nil {
			return err
		}
		// if numItems > 65535 {
		// 	return fmt.Errorf("Too many items in []uint64")
		// }

		// Read the number of items
		var items []uint64
		for i := uint16(0); i < numItems; i++ {
			var item uint64
			err = readElement(r, &item)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		*e = items
		return nil
	case *[]*btcec.Signature:
		var numSigs uint8
		err = readElement(r, &numSigs)
		if err != nil {
			return err
		}
		if numSigs > 127 {
			return fmt.Errorf("Too many signatures!")
		}

		// Read that number of signatures
		var sigs []*btcec.Signature
		for i := uint8(0); i < numSigs; i++ {
			sig := new(btcec.Signature)
			err = readElement(r, &sig)
			if err != nil {
				return err
			}
			sigs = append(sigs, sig)
		}
		*e = sigs
		return nil
	case **btcec.Signature:
		var sigLength uint8
		err = readElement(r, &sigLength)
		if err != nil {
			return err
		}

		if sigLength > 73 {
			return fmt.Errorf("Signature too long!")
		}

		// Read the sig length
		l := io.LimitReader(r, int64(sigLength))
		sig, err := ioutil.ReadAll(l)
		if err != nil {
			return err
		}
		if len(sig) != int(sigLength) {
			return fmt.Errorf("EOF: Signature length mismatch.")
		}
		btcecSig, err := btcec.ParseSignature(sig, btcec.S256())
		if err != nil {
			return err
		}
		*e = btcecSig
		return nil
	case *[]*[20]byte:
		// How many to read
		var sliceSize uint16
		err = readElement(r, &sliceSize)
		if err != nil {
			return err
		}
		var data []*[20]byte
		// Append the actual
		for i := uint16(0); i < sliceSize; i++ {
			var element [20]byte
			err = readElement(r, &element)
			if err != nil {
				return err
			}
			data = append(data, &element)
		}
		*e = data
		return nil
	case *[20]byte:
		_, err = io.ReadFull(r, e[:])
		if err != nil {
			return err
		}
		return nil
	case *wire.BitcoinNet:
		var b [4]byte
		_, err := io.ReadFull(r, b[:])
		if err != nil {
			return err
		}
		*e = wire.BitcoinNet(binary.BigEndian.Uint32(b[:]))
		return nil
	case *[]byte:
		// Get the blob length first
		var blobLength uint16
		err = readElement(r, &blobLength)
		if err != nil {
			return err
		}

		// Shouldn't need to do this, since it's uint16, but we
		// might have a different value for MAX_SLICE_LENGTH...
		if int(blobLength) > MAX_SLICE_LENGTH {
			return fmt.Errorf("Slice length too long!")
		}

		// Read the slice length
		l := io.LimitReader(r, int64(blobLength))
		*e, err = ioutil.ReadAll(l)
		if err != nil {
			return err
		}
		if len(*e) != int(blobLength) {
			return fmt.Errorf("EOF: Slice length mismatch.")
		}
		return nil
	case *PkScript:
		// Get the script length first
		var scriptLength uint8
		err = readElement(r, &scriptLength)
		if err != nil {
			return err
		}

		if scriptLength > 25 {
			return fmt.Errorf("PkScript too long!")
		}

		// Read the script length
		l := io.LimitReader(r, int64(scriptLength))
		*e, err = ioutil.ReadAll(l)
		if err != nil {
			return err
		}
		if len(*e) != int(scriptLength) {
			return fmt.Errorf("EOF: Signature length mismatch.")
		}
		return nil
	case *string:
		// Get the string length first
		var strlen uint16
		err = readElement(r, &strlen)
		if err != nil {
			return err
		}
		// Read the string for the length
		l := io.LimitReader(r, int64(strlen))
		b, err := ioutil.ReadAll(l)
		if len(b) != int(strlen) {
			return fmt.Errorf("EOF: String length mismatch.")
		}
		*e = string(b)
		if err != nil {
			return err
		}
		return nil
	case *[]*wire.TxIn:
		// Read the size (1-byte number of txins)
		var numScripts uint8
		err = readElement(r, &numScripts)
		if err != nil {
			return err
		}
		if numScripts > 127 {
			return fmt.Errorf("Too many txins")
		}

		// Append the actual TxIns
		var txins []*wire.TxIn
		for i := uint8(0); i < numScripts; i++ {
			outpoint := new(wire.OutPoint)
			txin := wire.NewTxIn(outpoint, nil, nil)
			err = readElement(r, &txin)
			if err != nil {
				return err
			}
			txins = append(txins, txin)
		}
		*e = txins
		return nil
	case **wire.TxIn:
		// Hash
		var h [32]byte
		_, err = io.ReadFull(r, h[:])
		if err != nil {
			return err
		}
		hash, err := wire.NewShaHash(h[:])
		if err != nil {
			return err
		}
		(*e).PreviousOutPoint.Hash = *hash
		// Index
		var idxBytes [4]byte
		_, err = io.ReadFull(r, idxBytes[:])
		if err != nil {
			return err
		}
		(*e).PreviousOutPoint.Index = binary.BigEndian.Uint32(idxBytes[:])
		return nil
	default:
		return fmt.Errorf("Unknown type in readElement: %T", e)
	}

	return nil
}

func readElements(r io.Reader, elements ...interface{}) error {
	for _, element := range elements {
		err := readElement(r, element)
		if err != nil {
			return err
		}
	}
	return nil
}

// validatePkScript determines if the passed pkScript is a valid pkScript within
// lnwire. The only pkScript templates that lnwire currently allows are:
// P2SH, P2WSH, P2PKH, and P2WKH.
func isValidPkScript(pkScript PkScript) bool {
	// A nil pkScript is obviously invalid.
	if pkScript == nil {
		return false
	}

	switch len(pkScript) {
	case 25:
		// A valid p2pkh script must be exactly 25 bytes. It must begin
		// with the define prefix, and end with the define suffix.
		p2pkhPrefix := []byte{txscript.OP_DUP, txscript.OP_HASH160}
		p2pkhSuffix := []byte{txscript.OP_EQUALVERIFY, txscript.OP_CHECKSIG,
			txscript.OP_DATA_20}
		if !bytes.Equal(pkScript[0:3], p2pkhPrefix) ||
			!bytes.Equal(pkScript[23:25], p2pkhSuffix) {
			return false
		}
	case 22:
		// P2WKH
		// A valid P2WKH script must be exactly 22 bytes, with the first
		// two op codes being an OP_0 marking a version zero witness
		// program, and the second byte being a 20 byte push data.
		if pkScript[0] != txscript.OP_0 ||
			pkScript[1] != txscript.OP_DATA_20 {
			return false
		}
	case 23:
		// A valid P2SH script must begin with OP_HASH160 PUSHDATA(20),
		// contain 20 bytes, then end with an OP_EQUAL.
		p2shPrefix := []byte{txscript.OP_HASH160, txscript.OP_DATA_20}
		p2shSuffix := []byte{txscript.OP_EQUAL}
		if !bytes.Equal(pkScript[0:2], p2shPrefix) ||
			!bytes.Equal(pkScript[22:23], p2shSuffix) {
			return false
		}
	case 34:
		// A P2WSH script must be exactly 34 bytes, with the first two
		// op codes being an OP_0 marking a version zero witness program,
		// and the second byte being a 32 byte push data.
		if pkScript[0] != txscript.OP_0 ||
			pkScript[1] != txscript.OP_DATA_32 {
			return false
		}
	default:
		return false
	}

	return true
}
