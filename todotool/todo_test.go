package todotool

import (
	"os"
	"testing"

	"github.com/ypsu/efftesting"
)

func TestNormalize(t *testing.T) {
	et := efftesting.New(t)
	f := func(s string) string { return efftesting.Stringify(normalizedate(s, "testitem")) }

	et.Expect("", f("20120332"), "20120401")
	et.Expect("", f("20120332.12"), "20120401.12")
	et.Expect("", f("20120332.1213"), "20120401.1213")
	et.Expect("", f("20120332.121314"), "20120401.121314")
	et.Expect("", f("20120332.121314.15"), "20120401.121314.15")
	et.Expect("", f("20120332.121314 blah"), "20120401.121314 blah")
	et.Expect("", f("20120332 blah"), "20120401 blah")
	et.Expect("", f("20120332.blah"), "20120401.blah")
	et.Expect("", f("20120332 foo.bar"), "20120401 foo.bar")
	et.Expect("", f("20991232 foo.bar"), "21000101 foo.bar")
	et.Expect("", f("20120332.121300"), "20120401.121300")
	et.Expect("", f("20120332.120000"), "20120401.120000")
	et.Expect("", f("20120332.000000"), "20120401.000000")
	et.Expect("", f("20120332.000000"), "20120401.000000")

	et.Expect("", f("blah"), "blah")
	et.Expect("", f("1203"), "1203")
}

func TestRFC2047(t *testing.T) {
	et := efftesting.New(t)
	et.Expect("", decodeRFC2047("=?utf-8?b?c3rFkWzFkSwgYmFuw6FuIGFs?= =?utf-8?Q?ma_di=C3=B3?= = =?utf-8?Q?al ma?= narancs"), "szőlő, banán alma dió = al ma narancs")
	et.Expect("", decodeRFC2047("=?UTF-8?Q?=F0=9F=93=86_Beginnen_Sie_das_Jahr_2025_mit_einem_strahlenden_L?= =?UTF-8?Q?=C3=A4cheln_=E2=80=93_und_mit_einer_einfachen_Terminbuchung!?="), "📆 Beginnen Sie das Jahr 2025 mit einem strahlenden Lächeln – und mit einer einfachen Terminbuchung!")
	et.Expect("", decodeRFC2047("=?utf-8?q?V=C3=A1ltson Digit=C3=A1lis =C3=81llampolg=C3=A1r alkalmaz=C3=A1sra vagy =C3=9Cgyf=C3=A9lkapu+-ra, hogy elektronikusan int=C3=A9zhesse az =C3=BCgyeit!?="), "Váltson Digitális Állampolgár alkalmazásra vagy Ügyfélkapu+-ra, hogy elektronikusan intézhesse az ügyeit!")
	et.Expect("", decodeRFC2047("=?iso-8859-2?Q?Fw:_Fi=F3k_megsz=FBn=E9se_/_t=F6rl=E9se_-_Account_removing?="), "Fw: Fiók megszûnése / törlése - Account removing")
	et.Expect("", decodeRFC2047("=?iso-8859-2?q?Fw:_Fi=F3k_megsz=FBn=E9se_/_t=F6rl=E9se_-_Account_removing?="), "Fw: Fiók megszûnése / törlése - Account removing")
}

func TestMain(m *testing.M) {
	os.Exit(efftesting.Main(m))
}
