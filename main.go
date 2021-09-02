package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

const (
	set2BitsMask = uint16(0b1100_0000_0000_0000)
	set3BitsMask = uint16(0b1110_0000_0000_0000)
	set4BitsMask = uint16(0b1111_0000_0000_0000)
	set5BitsMask = uint16(0b1111_1000_0000_0000)
	set6BitsMask = uint16(0b1111_1100_0000_0000)
	set7BitsMask = uint16(0b1111_1110_0000_0000)
	PUSH1        = 0x60
	PUSH32       = 0x7F

	INPUT_FILE = "/datadisk-slow/accounts-with-code.csv"
)

// bitvec is a bit vector which maps bytes in a program.
// An unset bit means the byte is an opcode, a set bit means
// it's data (i.e. argument of PUSHxx).
type bitvec []byte

var lookup = [8]byte{
	0x80, 0x40, 0x20, 0x10, 0x8, 0x4, 0x2, 0x1,
}

func (bits bitvec) set1(pos uint64) {
	bits[pos/8] |= lookup[pos%8]
}

func (bits bitvec) setN(flag uint16, pos uint64) {
	a := flag >> (pos % 8)
	bits[pos/8] |= byte(a >> 8)
	if b := byte(a); b != 0 {
		//	If the bit-setting affects the neighbouring byte, we can assign - no need to OR it,
		//	since it's the first write to that byte
		bits[pos/8+1] = b
	}
}

func (bits bitvec) set8(pos uint64) {
	a := byte(0xFF >> (pos % 8))
	bits[pos/8] |= a
	bits[pos/8+1] = ^a
}

func (bits bitvec) set16(pos uint64) {
	a := byte(0xFF >> (pos % 8))
	bits[pos/8] |= a
	bits[pos/8+1] = 0xFF
	bits[pos/8+2] = ^a
}

// codeSegment checks if the position is in a code segment.
func (bits *bitvec) codeSegment(pos uint64) bool {
	return ((*bits)[pos/8] & (0x80 >> (pos % 8))) == 0
}

// codeBitmap collects data locations in code.
func codeBitmap(code []byte) bitvec {
	// The bitmap is 4 bytes longer than necessary, in case the code
	// ends with a PUSH32, the algorithm will push zeroes onto the
	// bitvector outside the bounds of the actual code.
	bits := make(bitvec, len(code)/8+1+4)
	return codeBitmapInternal(code, bits)
}

// codeBitmapInternal is the internal implementation of codeBitmap.
// It exists for the purpose of being able to run benchmark tests
// without dynamic allocations affecting the results.
func codeBitmapInternal(code, bits bitvec) bitvec {
	for pc := uint64(0); pc < uint64(len(code)); {
		op := code[pc] //OpCode(code[pc])
		pc++
		if op < PUSH1 || op > PUSH32 {
			continue
		}
		numbits := op - PUSH1 + 1
		if numbits >= 8 {
			for ; numbits >= 16; numbits -= 16 {
				bits.set16(pc)
				pc += 16
			}
			for ; numbits >= 8; numbits -= 8 {
				bits.set8(pc)
				pc += 8
			}
		}
		switch numbits {
		case 1:
			bits.set1(pc)
			pc += 1
		case 2:
			bits.setN(set2BitsMask, pc)
			pc += 2
		case 3:
			bits.setN(set3BitsMask, pc)
			pc += 3
		case 4:
			bits.setN(set4BitsMask, pc)
			pc += 4
		case 5:
			bits.setN(set5BitsMask, pc)
			pc += 5
		case 6:
			bits.setN(set6BitsMask, pc)
			pc += 6
		case 7:
			bits.setN(set7BitsMask, pc)
			pc += 7
		}
	}
	return bits
}

func main() {
	opcodes := map[int]string{
		0x00: "STOP",
		0x01: "ADD",
		0x02: "MUL",
		0x03: "SUB",
		0x04: "DIV",
		0x05: "SDIV",
		0x06: "MOD",
		0x07: "SMOD",
		0x08: "ADDMOD",
		0x09: "MULMOD",
		0x0A: "EXP",
		0x0B: "SIGNEXTEND",
		0x10: "LT",
		0x11: "GT",
		0x12: "SLT",
		0x13: "SGT",
		0x14: "EQ",
		0x15: "ISZERO",
		0x16: "AND",
		0x17: "OR",
		0x18: "XOR",
		0x19: "NOT",
		0x1A: "BYTE",
		0x1B: "SHL",
		0x1C: "SHR",
		0x1D: "SAR",
		0x20: "SHA3",
		0x30: "ADDRESS",
		0x31: "BALANCE",
		0x32: "ORIGIN",
		0x33: "CALLER",
		0x34: "CALLVALUE",
		0x35: "CALLDATALOAD",
		0x36: "CALLDATASIZE",
		0x37: "CALLDATACOPY",
		0x38: "CODESIZE",
		0x39: "CODECOPY",
		0x3A: "GASPRICE",
		0x3B: "EXTCODESIZE",
		0x3C: "EXTCODECOPY",
		0x3D: "RETURNDATASIZE",
		0x3E: "RETURNDATACOPY",
		0x3F: "EXTCODEHASH",
		0x40: "BLOCKHASH",
		0x41: "COINBASE",
		0x42: "TIMESTAMP",
		0x43: "NUMBER",
		0x44: "DIFFICULTY",
		0x45: "GASLIMIT",
		0x46: "CHAINID",
		0x47: "SELFBALANCE",
		0x48: "BASEFEE",
		0x50: "POP",
		0x51: "MLOAD",
		0x52: "MSTORE",
		0x53: "MSTORE8",
		0x54: "SLOAD",
		0x55: "SSTORE",
		0x56: "JUMP",
		0x57: "JUMPI",
		0x58: "PC",
		0x59: "MSIZE",
		0x5A: "GAS",
		0x5B: "JUMPDEST",
		0x60: "PUSH1",
		0x61: "PUSH2",
		0x62: "PUSH3",
		0x63: "PUSH4",
		0x64: "PUSH5",
		0x65: "PUSH6",
		0x66: "PUSH7",
		0x67: "PUSH8",
		0x68: "PUSH9",
		0x69: "PUSH10",
		0x6A: "PUSH11",
		0x6B: "PUSH12",
		0x6C: "PUSH13",
		0x6D: "PUSH14",
		0x6E: "PUSH15",
		0x6F: "PUSH16",
		0x70: "PUSH17",
		0x71: "PUSH18",
		0x72: "PUSH19",
		0x73: "PUSH20",
		0x74: "PUSH21",
		0x75: "PUSH22",
		0x76: "PUSH23",
		0x77: "PUSH24",
		0x78: "PUSH25",
		0x79: "PUSH26",
		0x7A: "PUSH27",
		0x7B: "PUSH28",
		0x7C: "PUSH29",
		0x7D: "PUSH30",
		0x7E: "PUSH31",
		0x7F: "PUSH32",
		0x80: "DUP1",
		0x81: "DUP2",
		0x82: "DUP3",
		0x83: "DUP4",
		0x84: "DUP5",
		0x85: "DUP6",
		0x86: "DUP7",
		0x87: "DUP8",
		0x88: "DUP9",
		0x89: "DUP10",
		0x8A: "DUP11",
		0x8B: "DUP12",
		0x8C: "DUP13",
		0x8D: "DUP14",
		0x8E: "DUP15",
		0x8F: "DUP16",
		0x90: "SWAP1",
		0x91: "SWAP2",
		0x92: "SWAP3",
		0x93: "SWAP4",
		0x94: "SWAP5",
		0x95: "SWAP6",
		0x96: "SWAP7",
		0x97: "SWAP8",
		0x98: "SWAP9",
		0x99: "SWAP10",
		0x9A: "SWAP11",
		0x9B: "SWAP12",
		0x9C: "SWAP13",
		0x9D: "SWAP14",
		0x9E: "SWAP15",
		0x9F: "SWAP16",
		0xA0: "LOG0",
		0xA1: "LOG1",
		0xA2: "LOG2",
		0xA3: "LOG3",
		0xA4: "LOG4",
		0xF0: "CREATE",
		0xF1: "CALL",
		0xF2: "CALLCODE",
		0xF3: "RETURN",
		0xF4: "DELEGATECALL",
		0xF5: "CREATE2",
		0xFA: "STATICCALL",
		0xFD: "REVERT",
		0xFF: "SELFDESTRUCT"}

	data, err := ioutil.ReadFile(INPUT_FILE)
	if err != nil {
		panic(err)
	}
	data_str := string(data)
	data_lines := strings.Split(data_str, "\n")

	resultFile, err := os.OpenFile("./result.csv", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)

	if err != nil {
		panic(err)
	}

	defer resultFile.Close()
	for i := 0; i < len(data_lines); {
		fields := strings.Split(data_lines[i], ",")
		if len(fields) < 2 {
			break
		}
		address := strings.TrimSpace(fields[0])
		codeB64 := strings.TrimSpace(fields[1])
		code, err := base64.StdEncoding.DecodeString(codeB64)
		if err != nil {
			panic(err)
		}

		push0Count := 0
		deployCost1 := 0 // no PUSH0
		deployCost2 := 0 // PUSH0
		runTimeCost1 := 0
		runTimeCost2 := 0

		fmt.Println("Address:", address)

		bitmap := codeBitmap(code)
		for pc := 0; pc < len(code); {
			cs := bitmap.codeSegment(uint64(pc))
			b := make([]byte, 1)
			b[0] = code[pc]
			if cs { // code segment
				op_name := opcodes[int(code[pc])]
				fmt.Println("[", pc, "]", hex.EncodeToString(b), op_name)
			} else { // data
				fmt.Println("[", pc, "]", hex.EncodeToString(b), "data")
			}

			if code[pc] == PUSH1 {
				next_op := code[pc+1]
				if next_op == 0x00 {
					fmt.Println(">>> PUSH0")
					push0Count += 1
					deployCost1 += (2 * 200)
					deployCost2 += 200
					runTimeCost1 += 3
					runTimeCost2 += 2
				}
			}
			pc++
		}
		resultFile.WriteString(fmt.Sprintf("%s, %d, %d, %d, %d, %d\n", address, push0Count, deployCost1, deployCost2, runTimeCost1, runTimeCost2))
		i++
	}
}

func show_usage() {
	fmt.Println(os.Args[0], "- performs JUMPDEST analysis")
	fmt.Println("Usage:")
	fmt.Println("\t", os.Args[0], "<hex encoded bytecode>")
}
