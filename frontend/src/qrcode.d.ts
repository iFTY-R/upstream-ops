declare module "qrcode" {
  export interface QRCodeToDataURLOptions {
    errorCorrectionLevel?: "L" | "M" | "Q" | "H"
    margin?: number
    scale?: number
    width?: number
    color?: {
      dark?: string
      light?: string
    }
  }

  export function toDataURL(text: string, options?: QRCodeToDataURLOptions): Promise<string>
  export function toCanvas(canvas: HTMLCanvasElement, text: string, options?: QRCodeToDataURLOptions): Promise<void>

  const QRCode: {
    toDataURL: typeof toDataURL
    toCanvas: typeof toCanvas
  }

  export default QRCode
}
