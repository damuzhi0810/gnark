/*
Copyright © 2020 ConsenSys

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package frontend

import (
	"math/big"

	"github.com/consensys/gnark/backend"
	"github.com/consensys/gnark/backend/r1cs"
	"github.com/consensys/gnark/backend/r1cs/r1c"
	"github.com/consensys/gurvy"
)

// ConstraintSystem represents a Groth16 like circuit
type ConstraintSystem struct {
	publicVariables     []Variable       // public inputs
	secretVariables     []Variable       // private inputs
	internalVariables   []Variable       // internal variables
	coeffs              []big.Int        // list of unique coefficients.
	constraints         []r1c.R1C        // list of R1C that yield an output (for example v3 == v1 * v2, return v3)
	assertions          []r1c.R1C        // list of R1C that yield no output (for example ensuring v1 == v2)
	publicVariableNames []string         // public inputs names
	secretVariableNames []string         // private inputs names
	wireTags            map[int][]string // optional tags -- debug info
	oneTerm             r1c.Term
}

const initialCapacity = 1e6 // TODO that must be tuned. -build tags?

// newConstraintSystem outputs a new circuit
func newConstraintSystem() ConstraintSystem {

	cs := ConstraintSystem{
		publicVariableNames: make([]string, 1),
		publicVariables:     make([]Variable, 1),
		secretVariableNames: make([]string, 0),
		secretVariables:     make([]Variable, 0),
		internalVariables:   make([]Variable, 0, initialCapacity),
		coeffs:              make([]big.Int, 0),
		constraints:         make([]r1c.R1C, 0, initialCapacity),
		assertions:          make([]r1c.R1C, 0),
	}

	// first entry of circuit is backend.OneWire
	cs.publicVariableNames[0] = backend.OneWire
	cs.publicVariables[0] = Variable{false, backend.Public, 0, nil}
	cs.oneTerm = cs.Term(cs.publicVariables[0], bOne)

	// TODO tags will soon die.
	cs.wireTags = make(map[int][]string)

	return cs
}

var (
	bMinusOne = new(big.Int).SetInt64(-1)
	bZero     = new(big.Int)
	bOne      = new(big.Int).SetInt64(1)
	bTwo      = new(big.Int).SetInt64(2)
)

func (cs *ConstraintSystem) Term(v Variable, coeff *big.Int) r1c.Term {
	if v.visibility == backend.Unset {
		panic("variable is not allocated.")
	}
	term := r1c.Pack(v.id, cs.coeffID(coeff), v.visibility)
	if coeff.Cmp(bZero) == 0 {
		term.SetCoeffValue(0)
	} else if coeff.Cmp(bOne) == 0 {
		term.SetCoeffValue(1)
	} else if coeff.Cmp(bTwo) == 0 {
		term.SetCoeffValue(2)
	} else if coeff.Cmp(bMinusOne) == 0 {
		term.SetCoeffValue(-1)
	}
	return term
}

// toR1CS constructs a rank-1 constraint sytem
func (cs *ConstraintSystem) toR1CS(curveID gurvy.ID) r1cs.R1CS {

	// wires = intermediatevariables | secret inputs | public inputs

	// setting up the result
	res := r1cs.UntypedR1CS{
		NbWires:         len(cs.internalVariables) + len(cs.publicVariables) + len(cs.secretVariables),
		NbPublicWires:   len(cs.publicVariables),
		NbSecretWires:   len(cs.secretVariables),
		NbConstraints:   len(cs.constraints) + len(cs.assertions),
		NbCOConstraints: len(cs.constraints),
		Constraints:     make([]r1c.R1C, len(cs.constraints)+len(cs.assertions)),
		SecretWires:     cs.secretVariableNames,
		PublicWires:     cs.publicVariableNames,
		Coefficients:    cs.coeffs,
	}

	// computational constraints (= gates)
	copy(res.Constraints, cs.constraints)
	copy(res.Constraints[len(cs.constraints):], cs.assertions)

	// we just need to offset our ids, such that wires = [internalVariables | secretVariables | publicVariables]
	offsetIDs := func(exp r1c.LinearExpression) {
		for j := 0; j < len(exp); j++ {
			_, _, cID, cVisibility := exp[j].Unpack()
			switch cVisibility {
			case backend.Public:
				exp[j].SetConstraintID(cID + len(cs.internalVariables) + len(cs.secretVariables))
			case backend.Secret:
				exp[j].SetConstraintID(cID + len(cs.internalVariables))
			case backend.Unset:
				panic("shouldn't happen")
			}
		}
	}

	for i := 0; i < len(res.Constraints); i++ {
		offsetIDs(res.Constraints[i].L)
		offsetIDs(res.Constraints[i].R)
		offsetIDs(res.Constraints[i].O)
	}

	// wire tags
	res.WireTags = make(map[int][]string)
	for k, v := range cs.wireTags {
		res.WireTags[k] = make([]string, len(v))
		copy(res.WireTags[k], v)
	}

	if curveID == gurvy.UNKNOWN {
		return &res
	}

	return res.ToR1CS(curveID)
}

// coeffID tries to fetch the entry where b is if it exits, otherwise appends b to
// the list of coeffs and returns the corresponding entry
func (cs *ConstraintSystem) coeffID(b *big.Int) int {
	// TODO restore the map struct to avoid linear search of all coefficients to check existence
	idx := -1
	for i, v := range cs.coeffs {
		if v.Cmp(b) == 0 {
			idx = i
			return idx
		}
	}
	var toAppend big.Int
	toAppend.Set(b)
	cs.coeffs = append(cs.coeffs, toAppend)

	return len(cs.coeffs) - 1
}

// Tag assign a key to a variable to be able to monitor it when the system is solved
func (cs *ConstraintSystem) Tag(v Variable, tag string) {

	// TODO do we allOoutputw inputs to be tagged? anyway returns an error instead of panicing
	if v.visibility != backend.Internal {
		panic("inputs cannot be tagged")
	}

	for _, v := range cs.wireTags {
		for _, t := range v {
			if tag == t {
				panic("duplicate tag " + tag)
			}
		}
	}
	cs.wireTags[v.id] = append(cs.wireTags[v.id], tag)
}

// newInternalVariable creates a new wire, appends it on the list of wires of the circuit, sets
// the wire's id to the number of wires, and returns it
func (cs *ConstraintSystem) newInternalVariable() Variable {
	res := Variable{
		id:         len(cs.internalVariables),
		visibility: backend.Internal,
	}
	cs.internalVariables = append(cs.internalVariables, res)
	return res
}

// oneVariable returns the variable associated with backend.OneWire
func (cs *ConstraintSystem) oneVariable() Variable {
	return cs.publicVariables[0]
}
