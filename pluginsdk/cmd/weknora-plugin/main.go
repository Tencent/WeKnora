// Command weknora-plugin signs plugin packages.
//
//	weknora-plugin keygen -out acme.key          # prints the public key
//	weknora-plugin sign -key acme.key -key-id acme-2026 acme-search-1.0.0.wkp
//	weknora-plugin verify -pubkey ed25519:... acme-search-1.0.0.wkp
//
// Give the public key and key ID to the WeKnora platforms that should trust
// your packages; they list it in the file WEKNORA_PLUGIN_TRUSTED_KEYS names.
package main

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/pluginsdk/pluginsign"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "weknora-plugin:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: weknora-plugin keygen|sign|verify [flags]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "file to write the private key to (PKCS#8 PEM)")
	_ = fs.Parse(args)
	if *out == "" {
		return fmt.Errorf("-out is required")
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	pemBytes, err := pluginsign.MarshalPrivateKey(priv)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(pemBytes); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Println(pluginsign.FormatPublicKey(pub))
	return nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyFile := fs.String("key", "", "private key file from keygen")
	keyID := fs.String("key-id", "", "the key's ID in the platforms' trust stores")
	out := fs.String("out", "", "signed package to write (default: replace the input)")
	_ = fs.Parse(args)
	if *keyFile == "" || *keyID == "" || fs.NArg() != 1 {
		return fmt.Errorf("usage: weknora-plugin sign -key FILE -key-id ID PACKAGE.wkp")
	}
	keyPEM, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	priv, err := pluginsign.ParsePrivateKey(keyPEM)
	if err != nil {
		return err
	}
	in := fs.Arg(0)
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	signed, err := pluginsign.SignArchive(data, *keyID, priv)
	if err != nil {
		return err
	}
	dest := *out
	if dest == "" {
		dest = in
	}
	if err := os.WriteFile(dest, signed, 0o644); err != nil {
		return err
	}
	fmt.Printf("signed %s with %s\n", dest, *keyID)
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubText := fs.String("pubkey", "", "public key (ed25519:...)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: weknora-plugin verify [-pubkey KEY] PACKAGE.wkp")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	files, err := pluginsign.ReadArchive(data)
	if err != nil {
		return err
	}
	sig, err := pluginsign.Read(files)
	if err != nil {
		return err
	}
	if sig == nil {
		return fmt.Errorf("the package is not signed")
	}
	if *pubText == "" {
		if pluginsign.ContentDigest(files) != sig.ContentDigest {
			return pluginsign.ErrContentChanged
		}
		fmt.Printf("signed by %s; contents match (give -pubkey to check the signature)\n", sig.KeyID)
		return nil
	}
	pub, err := pluginsign.ParsePublicKey(*pubText)
	if err != nil {
		return err
	}
	if err := sig.Verify(files, pub); err != nil {
		return err
	}
	fmt.Printf("signature by %s verifies\n", sig.KeyID)
	return nil
}
